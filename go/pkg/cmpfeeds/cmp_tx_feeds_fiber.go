package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	fiber "github.com/chainbound/fiber-go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	"strings"
	"sync"
	"time"
)

// TxFeedsCompareFiberService represents a service which compares transaction feeds time difference
// between fiber gateway and BX cloud services.
type TxFeedsCompareFiberService struct {
	handlers  chan handler
	fiberCh   chan *message
	bxCh      chan *message
	bxBlockCh chan *message

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

	seenTxsInBlock []string
}

// NewTxFeedsCompareFiberService creates and initializes TxFeedsCompareFiberService instance.
func NewTxFeedsCompareFiberService() *TxFeedsCompareFiberService {
	const bufSize = 8192
	return &TxFeedsCompareFiberService{
		handlers:        make(chan handler),
		bxCh:            make(chan *message),
		fiberCh:         make(chan *message),
		bxBlockCh:       make(chan *message),
		hashes:          make(chan string, bufSize),
		lowFeeHashes:    utils.NewHashSet(),
		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),
		seenHashes:      make(map[string]*hashEntry),
	}
}

// Run is an entry point to the TxFeedsCompareFiberService.
func (s *TxFeedsCompareFiberService) Run(c *cli.Context) error {
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
				"TxHash", "BloXRoute Time", "Fiber Time", "Time Diff", "Mined In The Block",
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

	go s.readFeedFromFiber(
		ctx,
		s.fiberCh,
		c.String(flags.FiberUri.Name),
		c.String(flags.FiberAPIKey.Name),
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

func (s *TxFeedsCompareFiberService) handleUpdates(
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
			case data, ok := <-s.bxBlockCh:
				if !ok {
					fmt.Printf("failed to get data from bxblockch %v", data)
					continue
				}

				if err := s.processBlockFeedFromBX(data); err != nil {
					fmt.Printf("error: %v", err)
				}
			default:
				break
			}
		}
	}
}

func (s *TxFeedsCompareFiberService) processFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	var msg bxTxFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	txHash := msg.Params.Result.TxHash
	if txHash == "0x38e9501f8b973b4a89fa8f33a2e01db6256ffbf3faef1ec305880f976c8aad29" {
		fmt.Printf("found 38e9501f8b973b4a89fa8f33a2e01db6256ffbf3faef1ec305880f976c8aad29 in bloxroute")
	}
	log.Debugf("got message at %s (BXR node, ALL), txHash: %s", timeReceived, txHash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.bxrTimeReceived.IsZero() {
			entry.bxrTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &hashEntry{
			hash:            txHash,
			bxrTimeReceived: timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareFiberService) processFeedFromFiber(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	txHash := data.hash
	if txHash == "0x38e9501f8b973b4a89fa8f33a2e01db6256ffbf3faef1ec305880f976c8aad29" {
		fmt.Printf("found 38e9501f8b973b4a89fa8f33a2e01db6256ffbf3faef1ec305880f976c8aad29 in fiber")
	}
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

func (s *TxFeedsCompareFiberService) stats(ignoreDelta int, verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		txSeenByBothFeedsGatewayFirst      = float64(0)
		txSeenByBothFeedsFiberFirst        = float64(0)
		txReceivedByGatewayFirstTotalDelta = float64(0)
		txReceivedByFiberFirstTotalDelta   = float64(0)
		newTxFromGatewayFeedFirst          = 0
		newTxFromFiberFeedFirst            = 0
		totalTxFromGateway                 = 0
		totalTxFromFiber                   = 0
	)

	for txHash, entry := range s.seenHashes {
		var validTx bool
		for _, t := range s.seenTxsInBlock {
			if txHash == t {
				validTx = true
			}
		}
		if !validTx {
			if entry.bxrTimeReceived.IsZero() {
				fmt.Printf("fiber transaction %v was not found in a block\n", txHash)
			} else if entry.fiberTimeReceived.IsZero() {
				fmt.Printf("bloxroute transaction %v was not found in a block\n", txHash)
			} else {
				fmt.Printf("both fiber and bloxroute transaction %v was not found in a block\n", txHash)
			}
			continue
		}

		if entry.bxrTimeReceived.IsZero() {
			fiberTimeReceived := entry.fiberTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", fiberTimeReceived.Format(timestampFormat), "0", "1"}
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
				record := []string{txHash, gatewayTimeReceived.Format(timestampFormat), "0", "0", "1"}
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
				"1",
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(fiberTimeReceived):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Seconds()
		case fiberTimeReceived.Before(gatewayTimeReceived):
			newTxFromFiberFeedFirst++
			txSeenByBothFeedsFiberFirst++
			txReceivedByFiberFirstTotalDelta += timeReceivedDiff.Seconds()
		}
	}

	var (
		newTxSeenByBothFeeds = txSeenByBothFeedsGatewayFirst +
			txSeenByBothFeedsFiberFirst
		txReceivedByGatewayFirstAvgDelta = float64(0)
		txReceivedByFiberFirstAvgDelta   = float64(0)
		txPercentageSeenByGatewayFirst   = float64(0)
	)

	if txSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = txReceivedByGatewayFirstTotalDelta / txSeenByBothFeedsGatewayFirst
	}

	if txSeenByBothFeedsFiberFirst != 0 {
		txReceivedByFiberFirstAvgDelta = txReceivedByFiberFirstTotalDelta / txSeenByBothFeedsFiberFirst
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = txSeenByBothFeedsGatewayFirst / newTxSeenByBothFeeds
	}

	var timeAverage = txPercentageSeenByGatewayFirst*txReceivedByGatewayFirstAvgDelta - (1-txPercentageSeenByGatewayFirst)*txReceivedByFiberFirstAvgDelta
	results := fmt.Sprintf(
		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from bloXroute gateway first: %d\n"+
			"Number of transactions received from Fiber first: %d\n"+
			"Percentage of transactions seen first from bloXroute gateway: %.2f%%\n"+
			"Average time difference for transactions received first from bloXroute gateway (s): %f\n"+
			"Average time difference for transactions received first from Fiber (s): %f\n"+
			"Gateway is faster then Fiber by (s): %.2f\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from bloXroute gateway: %d\n"+
			"Total tx from Fiber: %d\n",

		int(newTxSeenByBothFeeds),
		int(txSeenByBothFeedsGatewayFirst),
		int(txSeenByBothFeedsFiberFirst),
		txPercentageSeenByGatewayFirst*100,
		txReceivedByGatewayFirstAvgDelta,
		txReceivedByFiberFirstAvgDelta,
		timeAverage,
		totalTxFromGateway,
		totalTxFromFiber,
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

func (s *TxFeedsCompareFiberService) readFeedFromBX(
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

func (s *TxFeedsCompareFiberService) readFeedFromFiber(
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
			timeReceived = time.Now()
			msg          = &message{
				hash:         tx.Hash.String(),
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

func (s *TxFeedsCompareFiberService) clearTrailNewHashes() {
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

func (s *TxFeedsCompareFiberService) readBlockFeedFromBX(
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
			return
		case out <- msg:
		}
	}
}

func (s *TxFeedsCompareFiberService) processBlockFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	var msg bxBkFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	hash := msg.Params.Result.Hash
	log.Debugf("got message at %s (BXR node, ALL), hash: %s", timeReceived, hash)

	var blockTxs []string
	for _, v := range msg.Params.Result.Transactions {
		blockTxs = append(blockTxs, fmt.Sprint(v["hash"]))
	}
	s.seenTxsInBlock = append(s.seenTxsInBlock, blockTxs...)

	return nil
}
