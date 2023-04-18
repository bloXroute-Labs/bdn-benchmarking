package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	pb "performance/pkg/cmpfeeds/protobuf"
	"strings"
	"sync"
	"time"
)

// TxGrpcWSCompareService represents a service which compares transaction feeds time difference
// between Ws and gRPC connection.
type TxGrpcWSCompareService struct {
	handlers chan handler
	bxCh     chan *message
	bxGrpcCh chan *message

	hashes chan string

	trailNewHashes        utils.HashSet
	leadNewHashes         utils.HashSet
	lowFeeHashes          utils.HashSet
	highDeltaHashes       utils.HashSet
	seenHashes            map[string]*grpcHashEntry
	timeToBeginComparison time.Time
	timeToEndComparison   time.Time
	numIntervals          int

	excTxContents bool
	minGasPrice   *float64
	addresses     utils.HashSet
	feedName      string

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer
}

// NewTxWsGrpcCompareService creates and initializes TxGrpcWSCompareService instance.
func NewTxWsGrpcCompareService() *TxGrpcWSCompareService {
	const bufSize = 8192
	return &TxGrpcWSCompareService{
		handlers:        make(chan handler),
		bxCh:            make(chan *message),
		bxGrpcCh:        make(chan *message),
		hashes:          make(chan string, bufSize),
		lowFeeHashes:    utils.NewHashSet(),
		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),
		seenHashes:      make(map[string]*grpcHashEntry),
	}
}

// Run is an entry point to the TxGrpcWSCompareService.
func (s *TxGrpcWSCompareService) Run(c *cli.Context) error {
	if mgp := c.Float64(flags.MinGasPrice.Name); mgp != 0.0 {
		s.minGasPrice = &mgp
	}

	if addr := c.String(flags.Addresses.Name); addr != "" {
		s.addresses = utils.NewHashSet()
		for _, addr := range strings.Split(strings.ToLower(addr), ",") {
			s.addresses[addr] = struct{}{}
		}
	}

	s.excTxContents = c.Bool(flags.ExcludeTxContents.Name)

	if (s.minGasPrice != nil || len(s.addresses) > 0) && s.excTxContents {
		return fmt.Errorf(
			"error: if filtering by minimum gas price or addresses, exclude-tx-contents must be false")
	}

	if s.minGasPrice != nil {
		*s.minGasPrice *= 10e8
	}

	if d := strings.ToUpper(c.String(flags.Dump.Name)); d != "" {
		const all, missing, allAndMissing = "ALL", "MISSING", "ALL,MISSING"
		if d != all && d != missing && d != allAndMissing {
			return fmt.Errorf(
				"error: possible values for --%s are %q, %q, %q",
				flags.Dump.Name, all, missing, allAndMissing)
		}

		if strings.Contains(d, all) {
			const fileName = "all_hashes.csv"
			file, err := os.Create(fileName)
			if err != nil {
				return fmt.Errorf("cannot open file %q: %v", fileName, err)
			}

			defer func() {
				if s.allHashesFile != nil {
					s.allHashesFile.Flush()
				}
				if err := file.Sync(); err != nil {
					log.Errorf("cannot sync contents of file %q: %v", fileName, err)
				}
				if err := file.Close(); err != nil {
					log.Errorf("cannot close file %q: %v", fileName, err)
				}
			}()

			s.allHashesFile = csv.NewWriter(file)

			if err := s.allHashesFile.Write([]string{
				"TxHash", "BloXRoute Time", "Grpc Time", "Time Diff",
			}); err != nil {
				return fmt.Errorf("cannot write CSV header of file %q: %v", fileName, err)
			}
		}

		if strings.Contains(d, missing) {
			const fileName = "missing_hashes.txt"
			file, err := os.Create(fileName)
			if err != nil {
				return fmt.Errorf("cannot open file %q: %v", fileName, err)
			}

			s.missingHashesFile = bufio.NewWriter(file)

			defer func() {
				if s.missingHashesFile != nil {
					if err := s.missingHashesFile.Flush(); err != nil {
						log.Errorf("cannot flush buffer contents of file %q: %v", fileName, err)
					}
				}
				if err := file.Sync(); err != nil {
					log.Errorf("cannot sync contents of file %q: %v", fileName, err)
				}
				if err := file.Close(); err != nil {
					log.Errorf("cannot close file %q: %v", fileName, err)
				}
			}()
		}
	}

	var (
		leadTimeSec  = c.Int(flags.LeadTime.Name)
		intervalSec  = c.Int(flags.Interval.Name)
		trailTimeSec = c.Int(flags.TrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	s.timeToBeginComparison = time.Now().Add(time.Second * time.Duration(leadTimeSec))
	s.timeToEndComparison = s.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
	s.numIntervals = c.Int(flags.NumIntervals.Name)
	s.feedName = c.String(flags.TxFeedName.Name)

	readerGroup.Add(2)

	go s.readFeedFromGRPC(ctx, &readerGroup, s.bxGrpcCh, c.String(flags.GatewayGrpc.Name))

	if c.Bool(flags.UseCloudAPI.Name) {
		go s.readFeedFromBX(
			ctx,
			&readerGroup,
			s.bxCh,
			c.String(flags.CloudAPIWSURI.Name),
			c.String(flags.AuthHeader.Name),
			c.Bool(flags.ExcludeDuplicates.Name),
			c.Bool(flags.ExcludeFromBlockchain.Name),
			false,
		)
	} else {
		go s.readFeedFromBX(
			ctx,
			&readerGroup,
			s.bxCh,
			c.String(flags.Gateway.Name),
			c.String(flags.AuthHeader.Name),
			c.Bool(flags.ExcludeDuplicates.Name),
			c.Bool(flags.ExcludeFromBlockchain.Name),
			c.Bool(flags.UseGoGateway.Name),
		)
	}

	handleGroup.Add(1)
	go s.handleUpdates(ctx, &handleGroup)

	time.Sleep(time.Second * time.Duration(leadTimeSec))
	for i := 0; i < s.numIntervals; i++ {
		time.Sleep(time.Second * time.Duration(intervalSec))
		s.clearTrailNewHashes()
		time.Sleep(time.Second * time.Duration(trailTimeSec))

		func(numIntervalsPassed int) {
			s.handlers <- func() error {
				msg := fmt.Sprintf(
					"-----------------------------------------------------\n"+
						"Interval (%d/%d): %d seconds. \n"+
						"End time: %s \n"+
						"Minimum gas price: %f \n"+
						"%s\n",
					numIntervalsPassed,
					s.numIntervals,
					intervalSec,
					time.Now().Format("2006-01-02T15:04:05.000"),
					c.Float64(flags.MinGasPrice.Name),
					s.stats(c.Int(flags.TxIgnoreDelta.Name),
						c.Bool(flags.Verbose.Name)),
				)

				s.drainChannels()

				s.seenHashes = make(map[string]*grpcHashEntry)
				s.leadNewHashes = utils.NewHashSet()
				s.timeToEndComparison = time.Now().Add(time.Second * time.Duration(intervalSec))

				fmt.Print(msg)

				if numIntervalsPassed == s.numIntervals {
					fmt.Printf("%d of %d intervals complete. Exiting.\n\n",
						numIntervalsPassed, s.numIntervals)
				}

				return nil
			}
		}(i + 1)
	}

	cancel()
	readerGroup.Wait()
	handleGroup.Wait()

	return nil
}

func (s *TxGrpcWSCompareService) handleUpdates(
	ctx context.Context,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-s.handlers:
			if !ok {
				continue
			}

			if err := update(); err != nil {
				log.Errorf("error in update function: %v", err)
			}
		default:
			select {
			case data, ok := <-s.bxCh:
				if !ok {
					continue
				}

				if err := s.processFeedFromBX(data); err != nil {
					log.Errorf("error: %v", err)
				}
			case data, ok := <-s.bxGrpcCh:
				if !ok {
					continue
				}

				if err := s.processFeedFromGRPC(data); err != nil {
					log.Errorf("failed to process feed from grpc with %v", err)
				}
			default:
				break
			}
		}
	}
}

func (s *TxGrpcWSCompareService) processFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	var msg bxTxFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	txHash := msg.Params.Result.TxHash
	log.Debugf("got message at %s (BXR node, ALL), txHash: %s", data.timeReceived, txHash)

	if data.timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if !s.excTxContents {
		to := msg.Params.Result.TxContents.To
		if !s.addresses.Empty() && to != nil && !s.addresses.Contains(*to) {
			return nil
		}

		if price := msg.Params.Result.TxContents.GasPrice; s.minGasPrice != nil && price != nil {
			gasPrice, err := parseGasPrice(*price)
			if err != nil {
				return fmt.Errorf("cannot parse gas price %q for transaction %q: %v",
					*price, txHash, err)
			}

			if float64(gasPrice) < *s.minGasPrice {
				s.lowFeeHashes.Add(txHash)
				return nil
			}
		}
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.bxrTimeReceived.IsZero() {
			entry.bxrTimeReceived = data.timeReceived
		}
	} else if data.timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &grpcHashEntry{
			hash:            txHash,
			bxrTimeReceived: data.timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxGrpcWSCompareService) processFeedFromGRPC(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v", s.feedName, data.err)
	}

	txHash := data.hash

	log.Debugf("got message at %s (BXR node, ALL), txHash: %s", data.timeReceived, txHash)
	if data.timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}
	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.grpcBxrTimeReceived.IsZero() {
			entry.grpcBxrTimeReceived = data.timeReceived
		}
	} else if data.timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {
		s.seenHashes[txHash] = &grpcHashEntry{
			hash:                txHash,
			grpcBxrTimeReceived: data.timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxGrpcWSCompareService) stats(ignoreDelta int, verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		txSeenByBothFeedsGatewayFirst          = int64(0)
		txSeenByBothFeedsGrpcGatewayFirst      = int64(0)
		txReceivedByGatewayFirstTotalDelta     = int64(0)
		txReceivedByGrpcGatewayFirstTotalDelta = int64(0)
		newTxFromGatewayFeedFirst              = 0
		newTxFromGrpcGatewayFeedFirst          = 0
		totalTxFromGateway                     = 0
		totalTxFromGrpcGateway                 = 0
	)

	for txHash, entry := range s.seenHashes {
		if entry.bxrTimeReceived.IsZero() {
			grpcTimeReceived := entry.grpcBxrTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", grpcTimeReceived.Format(timestampFormat), "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromGrpcGatewayFeedFirst++
			totalTxFromGrpcGateway++
			continue
		}
		if entry.grpcBxrTimeReceived.IsZero() {
			gatewayTimeReceived := entry.bxrTimeReceived

			if s.allHashesFile != nil {
				record := []string{txHash, gatewayTimeReceived.Format(timestampFormat), "0", "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromGatewayFeedFirst++
			totalTxFromGateway++
			continue
		}

		var (
			grpcGatewayTimeReceived = entry.grpcBxrTimeReceived
			gatewayTimeReceived     = entry.bxrTimeReceived
			timeReceivedDiff        = gatewayTimeReceived.Sub(grpcGatewayTimeReceived)
		)

		totalTxFromGateway++
		totalTxFromGrpcGateway++

		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			s.highDeltaHashes.Add(entry.hash)
			continue
		}

		if s.allHashesFile != nil {
			record := []string{
				txHash,
				gatewayTimeReceived.Format(timestampFormat),
				grpcGatewayTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Microseconds()),
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(grpcGatewayTimeReceived):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Microseconds()
		case grpcGatewayTimeReceived.Before(gatewayTimeReceived):
			newTxFromGrpcGatewayFeedFirst++
			txSeenByBothFeedsGrpcGatewayFirst++
			txReceivedByGrpcGatewayFirstTotalDelta += timeReceivedDiff.Microseconds()
		}
	}

	var (
		newTxSeenByBothFeeds                 = txSeenByBothFeedsGatewayFirst + txSeenByBothFeedsGrpcGatewayFirst
		txReceivedByGatewayFirstAvgDelta     = int64(0)
		txReceivedByGrpcGatewayFirstAvgDelta = int64(0)
		txPercentageSeenByGatewayFirst       = float64(0)
	)

	if txSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = txReceivedByGatewayFirstTotalDelta / txSeenByBothFeedsGatewayFirst
	}

	if txSeenByBothFeedsGrpcGatewayFirst != 0 {
		txReceivedByGrpcGatewayFirstAvgDelta = txReceivedByGrpcGatewayFirstTotalDelta / txSeenByBothFeedsGrpcGatewayFirst
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = float64(txSeenByBothFeedsGrpcGatewayFirst) / float64(newTxSeenByBothFeeds)
	}

	var timeAverage = txPercentageSeenByGatewayFirst*float64(txReceivedByGrpcGatewayFirstAvgDelta) - (1-txPercentageSeenByGatewayFirst)*float64(txReceivedByGatewayFirstAvgDelta)
	results := fmt.Sprintf(
		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from gRPC connection first: %d\n"+
			"Number of transactions received from websocket connection first: %d\n"+
			"Percentage of transactions seen first from gRPC connection: %.2f%%\n"+
			"Average time difference for transactions received first from gRPC connection (us): %d\n"+
			"Average time difference for transactions received first from websocket connection (us): %d\n"+
			"Final calculation (us): %.2f\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from gRPC connection: %d\n"+
			"Total tx from ws connection: %d\n"+
			"Number of low fee tx ignored: %d\n",

		newTxSeenByBothFeeds,
		txSeenByBothFeedsGrpcGatewayFirst,
		txSeenByBothFeedsGatewayFirst,
		txPercentageSeenByGatewayFirst*100,
		txReceivedByGrpcGatewayFirstAvgDelta,
		txReceivedByGatewayFirstAvgDelta,
		timeAverage,
		totalTxFromGrpcGateway,
		totalTxFromGateway,
		len(s.lowFeeHashes))

	verboseResults := fmt.Sprintf(
		"Number of high delta tx ignored: %d\n"+
			"Number of new transactions received first from gRPC connection: %d\n"+
			"Number of new transactions received first from websocket connection: %d\n"+
			"Total number of transactions seen: %d\n",
		len(s.highDeltaHashes),
		newTxFromGrpcGatewayFeedFirst,
		newTxFromGatewayFeedFirst,
		newTxFromGrpcGatewayFeedFirst+newTxFromGatewayFeedFirst,
	)

	if verbose {
		results += verboseResults
	}

	return results
}

func (s *TxGrpcWSCompareService) readFeedFromBX(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
	authHeader string,
	excDuplicates bool,
	excFromBlockchain bool,
	useGoGateway bool,
) {
	defer wg.Done()

	log.Infof("Initiating connection to: %s", uri)
	conn, err := ws.NewConnection(uri, authHeader)
	if err != nil {
		log.Errorf("cannot establish connection to %s: %v", uri, err)
		return
	}
	log.Infof("Connection to %s established", uri)

	defer func() {
		if err := conn.Close(); err != nil {
			log.Errorf("cannot close socket connection to %s: %v", uri, err)
		}
	}()

	sub, err := conn.SubscribeTxFeedBX(1, s.feedName, s.excTxContents, !excDuplicates,
		!excFromBlockchain, useGoGateway)
	if err != nil {
		log.Errorf("cannot subscribe to feed %q: %v", s.feedName, err)
		return
	}

	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from feed %q: %v", s.feedName, err)
		}
	}()

	for {
		var (
			data, err    = sub.NextMessage()
			timeReceived = time.Now()
			msg          = &message{
				bytes:        data,
				err:          err,
				timeReceived: timeReceived,
			}
		)

		select {
		case <-ctx.Done():
			return
		case out <- msg:
		}
	}
}

func (s *TxGrpcWSCompareService) clearTrailNewHashes() {
	done := make(chan struct{})
	go func() {
		s.handlers <- func() error {
			s.trailNewHashes = utils.NewHashSet()
			done <- struct{}{}
			return nil
		}
	}()
	<-done
}

func (s *TxGrpcWSCompareService) drainChannels() {
	done := make(chan struct{})
	go func() {
		for len(s.hashes) > 0 {
			<-s.hashes
		}

		for len(s.bxGrpcCh) > 0 {
			<-s.bxGrpcCh
		}

		done <- struct{}{}
	}()
	<-done
}

func (s *TxGrpcWSCompareService) readFeedFromGRPC(ctx context.Context, wg *sync.WaitGroup, out chan<- *message, uri string) {
	defer wg.Done()

	log.Infof("Initiating connection to GRPC %v", uri)

	conn, err := grpc.Dial(uri, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	client := pb.NewGatewayClient(conn)
	callContext, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	log.Infof("Connection to %s established", uri)
	switch s.feedName {
	case "newTxs":
		stream, err := client.NewTxs(callContext, &pb.NewTxsRequest{Filters: ""})
		if err != nil {
			log.Errorf("could not create newTxs %v", err)
		}
		for {
			data, err := stream.Recv()
			timeReceived := time.Now()
			if err != nil {
				log.Errorf("error in recieve: %v", err)
			}
			if data != nil {
				var (
					msg = &message{
						hash:         data.Tx[0].Hash,
						err:          err,
						timeReceived: timeReceived,
					}
				)

				select {
				case <-ctx.Done():
					return
				case out <- msg:
				}
			}
		}
	case "pendingTxs":
		stream, err := client.PendingTxs(callContext, &pb.PendingTxsRequest{Filters: ""})
		if err != nil {
			log.Errorf("could not create pendingTxs %v", err)
		}
		for {
			data, err := stream.Recv()
			timeReceived := time.Now()
			if err != nil {
				log.Errorf("error in recieve: %v", err)
			}
			if data != nil {
				var (
					msg = &message{
						hash:         data.Tx[0].Hash,
						err:          err,
						timeReceived: timeReceived,
					}
				)

				select {
				case <-ctx.Done():
					return
				case out <- msg:
				}
			}
		}
	default:
		log.Errorf("invalid feed name provided")
		os.Exit(1)
	}
}
