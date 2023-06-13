package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	pb "performance/pkg/cmpfeeds/protobuf"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type wsVsGRPCHashes struct {
	wsTimeReceived   time.Time
	grpcTimeReceived time.Time
	hash             string
	wsMessageLen     int
	grpcMessageLen   int
}

// TxFeedsCompareWsVsGRPC represents a service which compares transaction feeds time difference
type TxFeedsCompareWsVsGRPC struct {
	handlers  chan handler
	grpcCh    chan *message
	wsCh      chan *message
	bxBlockCh chan *message

	trailNewHashes        utils.HashSet
	leadNewHashes         utils.HashSet
	highDeltaHashes       utils.HashSet
	seenHashes            map[string]*wsVsGRPCHashes
	timeToBeginComparison time.Time
	timeToEndComparison   time.Time
	numIntervals          int

	excTxContents bool
	minGasPrice   *float64
	addresses     utils.HashSet
	feedName      string

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer

	seenTxsInBlock []string
}

// NewTxFeedsCompareWsVsGRPC creates and initializes TxFeedsCompareWsVsGRPC instance.
func NewTxFeedsCompareWsVsGRPC() *TxFeedsCompareWsVsGRPC {
	return &TxFeedsCompareWsVsGRPC{
		handlers:  make(chan handler),
		wsCh:      make(chan *message, 10000),
		grpcCh:    make(chan *message, 10000),
		bxBlockCh: make(chan *message, 10000),

		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),

		seenHashes: make(map[string]*wsVsGRPCHashes),
	}
}

// Run is an entry point to the TxFeedsCompareWsVsGRPC.
func (s *TxFeedsCompareWsVsGRPC) Run(c *cli.Context) error {
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
				"TxHash", "WS Time", "GRPC Time", "Time Diff", "WS message len", "GRPC message len",
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

	go s.readFeedFromGateway(
		ctx,
		&readerGroup,
		s.wsCh,
		c.String(flags.Gateway.Name),
		c.String(flags.AuthHeader.Name),
		c.Bool(flags.ExcludeDuplicates.Name),
		c.Bool(flags.ExcludeFromBlockchain.Name),
		c.Bool(flags.UseGoGateway.Name),
	)
	go s.readFeedFromGRPCBX(
		ctx,
		&readerGroup,
		s.grpcCh,
		c.String(flags.GRPCURI.Name),
	)

	go s.readBlockFeedFromBX(
		ctx,
		&readerGroup,
		s.bxBlockCh,
		c.String(flags.Gateway.Name),
		c.String(flags.AuthHeader.Name),
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

				s.seenHashes = make(map[string]*wsVsGRPCHashes)
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

func (s *TxFeedsCompareWsVsGRPC) handleUpdates(
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
		case data, ok := <-s.wsCh:
			if !ok {
				continue
			}

			if err := s.processFeedFromWS(data); err != nil {
				log.Errorf("error: %v", err)
			}
		case data, ok := <-s.grpcCh:
			if !ok {
				continue
			}

			if err := s.processFeedFromGRPC(data); err != nil {
				log.Errorf("error: %v", err)
			}
		case data, ok := <-s.bxBlockCh:
			if !ok {
				fmt.Printf("failed to get data from bxblockch %v", data)
				continue
			}

			if err := s.processBlockFeedFromBX(data); err != nil {
				fmt.Printf("error: %v", err)
			}
		}
	}
}

func (s *TxFeedsCompareWsVsGRPC) processFeedFromWS(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := data.timeReceived

	var msg bxTxFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	txHash := msg.Params.Result.TxHash

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.wsTimeReceived.IsZero() {
			entry.wsTimeReceived = timeReceived
			entry.wsMessageLen = data.gwMessageLen
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &wsVsGRPCHashes{
			hash:           txHash,
			wsTimeReceived: timeReceived,
			wsMessageLen:   data.gwMessageLen,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareWsVsGRPC) processFeedFromGRPC(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := data.timeReceived

	txHash := hexutil.Encode(data.bytes)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.grpcTimeReceived.IsZero() {
			entry.grpcTimeReceived = timeReceived
			entry.grpcMessageLen = data.gwMessageLen
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &wsVsGRPCHashes{
			hash:             txHash,
			grpcTimeReceived: timeReceived,
			grpcMessageLen:   data.gwMessageLen,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareWsVsGRPC) stats(ignoreDelta int, verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		txSeenByBothFeedsGatewayFirst      = float64(0)
		txSeenByBothFeedsMEVLinkFirst      = float64(0)
		txReceivedByGatewayFirstTotalDelta = float64(0)
		txReceivedByMEVLinkFirstTotalDelta = float64(0)
		newTxFromGatewayFeedFirst          = 0
		newTxFromMEVLinkFeedFirst          = 0
		totalTxFromGateway                 = 0
		totalTxFromMEVLink                 = 0
		totalBytesFromWS                   = 0
		totalBytesFromGRPC                 = 0
	)

	for txHash, entry := range s.seenHashes {
		var validTx bool
		for _, t := range s.seenTxsInBlock {
			if txHash == t {
				validTx = true
			}
		}
		if !validTx {
			if entry.wsTimeReceived.IsZero() {
				log.Debugf("mevLink transaction %v was not found in a block\n", txHash)
			} else if entry.grpcTimeReceived.IsZero() {
				log.Debugf("bloxroute transaction %v was not found in a block\n", txHash)
			} else {
				log.Debugf("both mev link and bloxroute transaction %v was not found in a block\n", txHash)
			}
			continue
		}

		totalBytesFromWS += entry.wsMessageLen
		totalBytesFromGRPC += entry.grpcMessageLen

		if entry.wsTimeReceived.IsZero() {
			grpcTimeReceived := entry.grpcTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", grpcTimeReceived.Format(timestampFormat), "0", "1"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromMEVLinkFeedFirst++
			totalTxFromMEVLink++
			continue
		}
		if entry.grpcTimeReceived.IsZero() {
			wsTimeReceived := entry.wsTimeReceived

			if s.allHashesFile != nil {
				record := []string{txHash, wsTimeReceived.Format(timestampFormat), "0", "0", "1"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromGatewayFeedFirst++
			totalTxFromGateway++
			continue
		}

		var (
			grpcTimeReceived = entry.grpcTimeReceived
			wsTimeReceived   = entry.wsTimeReceived
			timeReceivedDiff = wsTimeReceived.Sub(grpcTimeReceived)
		)

		totalTxFromGateway++
		totalTxFromMEVLink++

		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			s.highDeltaHashes.Add(entry.hash)
			continue
		}

		if s.allHashesFile != nil {
			record := []string{
				txHash,
				wsTimeReceived.Format(timestampFormat),
				grpcTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Microseconds()),
				strconv.Itoa(entry.wsMessageLen),
				strconv.Itoa(entry.grpcMessageLen),
			}

			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case wsTimeReceived.Before(grpcTimeReceived):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Seconds()
		case grpcTimeReceived.Before(wsTimeReceived):
			newTxFromMEVLinkFeedFirst++
			txSeenByBothFeedsMEVLinkFirst++
			txReceivedByMEVLinkFirstTotalDelta += timeReceivedDiff.Seconds()
		}
	}

	var (
		newTxSeenByBothFeeds = txSeenByBothFeedsGatewayFirst +
			txSeenByBothFeedsMEVLinkFirst
		txReceivedByGatewayFirstAvgDelta = float64(0)
		txReceivedByMevLinkFirstAvgDelta = float64(0)
		txPercentageSeenByGatewayFirst   = float64(0)
	)

	if txSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = txReceivedByGatewayFirstTotalDelta / txSeenByBothFeedsGatewayFirst
	}

	if txSeenByBothFeedsMEVLinkFirst != 0 {
		txReceivedByMevLinkFirstAvgDelta = txReceivedByMEVLinkFirstTotalDelta / txSeenByBothFeedsMEVLinkFirst
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = txSeenByBothFeedsGatewayFirst / newTxSeenByBothFeeds
	}

	var timeAverage = txPercentageSeenByGatewayFirst*txReceivedByGatewayFirstAvgDelta - (1-txPercentageSeenByGatewayFirst)*txReceivedByMevLinkFirstAvgDelta
	results := fmt.Sprintf(
		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from WS feed first: %d\n"+
			"Number of transactions received from GRPC first: %d\n"+
			"Percentage of transactions seen first from WS gateway: %.2f%%\n"+
			"Average time difference for transactions received first from WS feed (ms): %f\n"+
			"Average time difference for transactions received first from GRPC feed (ms): %f\n"+
			"WS feed is faster then GRPC by (ms): %.6f\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from WS feed: %d\n"+
			"Total tx from GRPC feed: %d\n"+
			"Total bytes from WS: %d\n"+
			"Total bytes from GRPC: %d\n",

		int(newTxSeenByBothFeeds),
		int(txSeenByBothFeedsGatewayFirst),
		int(txSeenByBothFeedsMEVLinkFirst),
		txPercentageSeenByGatewayFirst*100,
		txReceivedByGatewayFirstAvgDelta*1000,
		txReceivedByMevLinkFirstAvgDelta*1000,
		timeAverage*1000,
		totalTxFromGateway,
		totalTxFromMEVLink,
		totalBytesFromWS,
		totalBytesFromGRPC,
	)

	return results
}

func (s *TxFeedsCompareWsVsGRPC) readFeedFromGateway(
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
				gwMessageLen: len(data),
			}
		)

		select {
		case <-ctx.Done():
			log.Info("Stop gateway feed")
			return
		case out <- msg:
		}
	}
}

func (s *TxFeedsCompareWsVsGRPC) readFeedFromGRPCBX(ctx context.Context, wg *sync.WaitGroup, out chan<- *message, uri string) {
	defer wg.Done()

	log.Infof("Initiating connection to GRPC %v", uri)

	conn, err := grpc.Dial(uri,
		grpc.WithWriteBufferSize(0),
		grpc.WithInitialConnWindowSize(128), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	client := pb.NewGatewayClient(conn)
	callContext, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	log.Infof("Connection to %s established", uri)

	stream, err := client.NewTxs(callContext, &pb.TxsRequest{Filters: ""})
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

			for _, tx := range data.Tx {
				var (
					msg = &message{
						bytes:        tx.Hash,
						timeReceived: timeReceived,
						gwMessageLen: proto.Size(tx),
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
}

func (s *TxFeedsCompareWsVsGRPC) readBlockFeedFromBX(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
	authHeader string,
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

	sub, err := conn.SubscribeBkFeedBX(1, "bdnBlocks", false)

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
			data, err = sub.NextMessage()
			msg       = &message{
				bytes: data,
				err:   err,
			}
		)

		select {
		case <-ctx.Done():
			log.Info("Stop blocks feed")
			return
		case out <- msg:
		}
	}
}

func (s *TxFeedsCompareWsVsGRPC) processBlockFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	var msg bxBkFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	h := msg.Params.Result.Hash
	log.Debugf("got message at %s (BXR node, ALL), hash: %s", timeReceived, h)

	var blockTxs []string
	for _, v := range msg.Params.Result.Transactions {
		blockTxs = append(blockTxs, fmt.Sprint(v["hash"]))
	}
	s.seenTxsInBlock = append(s.seenTxsInBlock, blockTxs...)

	return nil
}

func (s *TxFeedsCompareWsVsGRPC) clearTrailNewHashes() {
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
