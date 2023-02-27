package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	fiber "github.com/chainbound/fiber-go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	pb "performance/pkg/cmpfeeds/protobuf"
	"strings"
	"sync"
	"time"
)

// TxFeedsCompareFiberGrpcService represents a service which compares transaction feeds time difference
// between fiber gateway and BX cloud services.
type TxFeedsCompareFiberGrpcService struct {
	handlers chan handler
	fiberCh  chan *message
	bxCh     chan *message

	hashes chan string

	trailNewHashes        utils.HashSet
	leadNewHashes         utils.HashSet
	lowFeeHashes          utils.HashSet
	highDeltaHashes       utils.HashSet
	seenHashes            map[string]*hashEntry
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

// NewTxFeedsCompareFiberGrpcService creates and initializes TxFeedsCompareFiberGrpcService instance.
func NewTxFeedsCompareFiberGrpcService() *TxFeedsCompareFiberGrpcService {
	const bufSize = 8192
	return &TxFeedsCompareFiberGrpcService{
		handlers:        make(chan handler),
		bxCh:            make(chan *message),
		fiberCh:         make(chan *message),
		hashes:          make(chan string, bufSize),
		lowFeeHashes:    utils.NewHashSet(),
		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),
		seenHashes:      make(map[string]*hashEntry),
	}
}

// Run is an entry point to the TxFeedsCompareFiberGrpcService.
func (s *TxFeedsCompareFiberGrpcService) Run(c *cli.Context) error {
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
				"TxHash", "BloXRoute Time", "Fiber Time", "Time Diff",
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
		trailTimeSec = c.Int(flags.TxTrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	s.timeToBeginComparison = time.Now().Add(time.Second * time.Duration(leadTimeSec))
	s.timeToEndComparison = s.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
	s.numIntervals = c.Int(flags.NumIntervals.Name)
	s.feedName = c.String(flags.TxFeedName.Name)

	readerGroup.Add(2)
	go s.readFeedFromBX(
		ctx,
		&readerGroup,
		s.bxCh,
		c.String(flags.Gateway.Name),
	)

	go s.readFeedFromFiber(
		ctx,
		s.fiberCh,
		c.String(flags.FiberUri.Name),
		c.String(flags.FiberAPIKey.Name),
	)

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

				s.seenHashes = make(map[string]*hashEntry)
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

func (s *TxFeedsCompareFiberGrpcService) handleUpdates(
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
			case data, ok := <-s.fiberCh:
				if !ok {
					continue
				}

				if err := s.processFeedFromFiber(data); err != nil {
					log.Errorf("error: %v", err)
				}
			default:
				break
			}
		}
	}
}

func (s *TxFeedsCompareFiberGrpcService) processFeedFromBX(data *message) error {
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
		if entry.bxrTimeReceived.IsZero() {
			entry.bxrTimeReceived = data.timeReceived
		}
	} else if data.timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {
		s.seenHashes[txHash] = &hashEntry{
			hash:            txHash,
			bxrTimeReceived: data.timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareFiberGrpcService) processFeedFromFiber(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	txHash := data.hash
	log.Debugf("got message at %s (BXR node, ALL), txHash: %s", timeReceived, txHash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.fiberTimeReceived.IsZero() {
			entry.fiberTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &hashEntry{
			hash:              txHash,
			fiberTimeReceived: timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareFiberGrpcService) stats(ignoreDelta int, verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		txSeenByBothFeedsGatewayFirst      = int64(0)
		txSeenByBothFeedsFiberFirst        = int64(0)
		txReceivedByGatewayFirstTotalDelta = int64(0)
		txReceivedByFiberFirstTotalDelta   = int64(0)
		newTxFromGatewayFeedFirst          = 0
		newTxFromFiberFeedFirst            = 0
		totalTxFromGateway                 = 0
		totalTxFromFiber                   = 0
	)

	for txHash, entry := range s.seenHashes {
		if entry.bxrTimeReceived.IsZero() {
			fiberTimeReceived := entry.fiberTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", fiberTimeReceived.Format(timestampFormat), "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromFiberFeedFirst++
			totalTxFromFiber++
			continue
		}
		if entry.fiberTimeReceived.IsZero() {
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
			fiberTimeReceived   = entry.fiberTimeReceived
			gatewayTimeReceived = entry.bxrTimeReceived
			timeReceivedDiff    = gatewayTimeReceived.Sub(fiberTimeReceived)
		)

		totalTxFromGateway++
		totalTxFromFiber++

		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			s.highDeltaHashes.Add(entry.hash)
			continue
		}

		if s.allHashesFile != nil {
			record := []string{
				txHash,
				gatewayTimeReceived.Format(timestampFormat),
				fiberTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Milliseconds()),
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(fiberTimeReceived):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Microseconds()
		case fiberTimeReceived.Before(gatewayTimeReceived):
			newTxFromFiberFeedFirst++
			txSeenByBothFeedsFiberFirst++
			txReceivedByFiberFirstTotalDelta += timeReceivedDiff.Microseconds()
		}
	}

	var (
		newTxSeenByBothFeeds = txSeenByBothFeedsGatewayFirst +
			txSeenByBothFeedsFiberFirst
		txReceivedByGatewayFirstAvgDelta = int64(0)
		txReceivedByFiberFirstAvgDelta   = int64(0)
		txPercentageSeenByGatewayFirst   = float64(0)
	)

	if txSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = txReceivedByGatewayFirstTotalDelta / txSeenByBothFeedsGatewayFirst
	}

	if txSeenByBothFeedsFiberFirst != 0 {
		txReceivedByFiberFirstAvgDelta = txReceivedByFiberFirstTotalDelta / txSeenByBothFeedsFiberFirst
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = float64(txSeenByBothFeedsGatewayFirst) / float64(newTxSeenByBothFeeds)
	}

	var timeAverage = txPercentageSeenByGatewayFirst*float64(txReceivedByGatewayFirstAvgDelta) - (1-txPercentageSeenByGatewayFirst)*float64(txReceivedByFiberFirstAvgDelta)
	results := fmt.Sprintf(
		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from gRPC connection first: %d\n"+
			"Number of transactions received from Fiber first: %d\n"+
			"Percentage of transactions seen first from gRPC connection: %.2f%%\n"+
			"Average time difference for transactions received first from gRPC connection (us): %d\n"+
			"Average time difference for transactions received first from Fiber (us): %d\n"+
			"Final calculation (us): %.2f\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from gRPC connection: %d\n"+
			"Total tx from Fiber: %d\n"+
			"Number of low fee tx ignored: %d\n",

		newTxSeenByBothFeeds,
		txSeenByBothFeedsGatewayFirst,
		txSeenByBothFeedsFiberFirst,
		txPercentageSeenByGatewayFirst*100,
		txReceivedByGatewayFirstAvgDelta,
		txReceivedByFiberFirstAvgDelta,
		timeAverage,
		totalTxFromGateway,
		totalTxFromFiber,
		len(s.lowFeeHashes),
	)

	verboseResults := fmt.Sprintf(
		"Number of high delta tx ignored: %d\n"+
			"Number of new transactions received first from gateway: %d\n"+
			"Number of new transactions received first from node: %d\n"+
			"Total number of transactions seen: %d\n",
		len(s.highDeltaHashes),
		newTxFromGatewayFeedFirst,
		newTxFromFiberFeedFirst,
		newTxFromFiberFeedFirst+newTxFromGatewayFeedFirst,
	)

	if verbose {
		results += verboseResults
	}

	return results
}

func (s *TxFeedsCompareFiberGrpcService) readFeedFromBX(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
) {
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
}

func (s *TxFeedsCompareFiberGrpcService) readFeedFromFiber(
	ctx context.Context,
	out chan<- *message,
	uri string,
	authHeader string,
) {
	log.Infof("Initiating connection to: %s", uri)

	client := fiber.NewClient(uri, authHeader)
	// Close the client when you're done to make sure API accounting is done correctly
	defer client.Close()

	// Configure a timeout for establishing connection
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		log.Errorf("cannot establish connection to %s: %v", uri, err)
		return
	}

	log.Infof("Connection to %s established", uri)

	ch := make(chan *fiber.Transaction)
	go func() {
		// This is a blocking call, so it needs to run in a Goroutine
		if err := client.SubscribeNewTxs(nil, ch); err != nil {
			log.Errorf("Fiber - cannot subscribe to feed %q: %v", s.feedName, err)
			return
		}
	}()

	// Listen for incoming transactions
	for tx := range ch {
		var (
			msg = &message{
				hash: tx.Hash.String(),
			}
		)

		select {
		case <-ctx.Done():
			return
		case out <- msg:
		}
	}
}

func (s *TxFeedsCompareFiberGrpcService) clearTrailNewHashes() {
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
