package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TxFeedsCompareService represents a service which compares transaction feeds time difference
// between ETH node and BX gateway.
type TxFeedsCompareService struct {
	handlers chan handler
	ethCh    chan *message
	ethTxCh  chan *message
	bxCh     chan *message
	bxCh2    chan *message

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

// NewTxFeedsCompareService creates and initializes TxFeedsCompareService instance.
func NewTxFeedsCompareService() *TxFeedsCompareService {
	const bufSize = 8192
	return &TxFeedsCompareService{
		handlers:        make(chan handler),
		bxCh:            make(chan *message),
		bxCh2:           make(chan *message),
		ethCh:           make(chan *message),
		ethTxCh:         make(chan *message, bufSize),
		hashes:          make(chan string, bufSize),
		lowFeeHashes:    utils.NewHashSet(),
		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),
		seenHashes:      make(map[string]*hashEntry),
	}
}

// Run is an entry point to the TxFeedsCompareService.
func (s *TxFeedsCompareService) Run(c *cli.Context) error {
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
				"TxHash", "BloXRoute Time", "Eth Time", "Time Diff",
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
		//ethURI       = c.String(flags.Eth.Name)
		ctx, cancel = context.WithCancel(context.Background())

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
	go s.readFeedFromBX(
		ctx,
		&readerGroup,
		s.bxCh2,
		c.String(flags.Gateway2.Name),
		c.String(flags.AuthHeader.Name),
		c.Bool(flags.ExcludeDuplicates.Name),
		c.Bool(flags.ExcludeFromBlockchain.Name),
		c.Bool(flags.UseGoGateway.Name),
	)
	//go s.readFeedFromEth(ctx, &readerGroup, s.ethCh, ethURI)

	//if !s.excTxContents {
	//	const totalReaders = 4
	//	for i := 0; i < totalReaders; i++ {
	//		readerGroup.Add(1)
	//		go s.readTxContentsFromEth(
	//			ctx,
	//			&readerGroup,
	//			s.ethTxCh,
	//			ethURI,
	//		)
	//	}
	//}

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

func (s *TxFeedsCompareService) handleUpdates(
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
		case data, ok := <-s.ethTxCh:
			if !ok {
				continue
			}

			if err := s.processTxContentsFromEth(data); err != nil {
				log.Errorf("error: %v", err)
			}
		default:
			select {
			case data, ok := <-s.bxCh:
				if !ok {
					continue
				}

				if err := s.processFeedFromBX(data, 1); err != nil {
					log.Errorf("error: %v", err)
				}
			case data, ok := <-s.bxCh2:
				if !ok {
					continue
				}

				if err := s.processFeedFromBX(data, 2); err != nil {
					log.Errorf("error: %v", err)
				}
			case data, ok := <-s.ethCh:
				if !ok {
					continue
				}

				if err := s.processFeedFromEth(data); err != nil {
					log.Errorf("error: %v", err)
				}
			default:
				break
			}
		}
	}
}

func (s *TxFeedsCompareService) processFeedFromBX(data *message, gw int) error {
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
	log.Debugf("got message at %s (BXR node, ALL), txHash: %s", timeReceived, txHash)

	//if timeReceived.Before(s.timeToBeginComparison) {
	//	s.leadNewHashes.Add(txHash)
	//	return nil
	//}

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
		if gw == 1 {
			if entry.bxrTimeReceived.IsZero() {
				entry.bxrTimeReceived = timeReceived
			}
		} else {
			if entry.bxrTimeReceived2.IsZero() {
				entry.bxrTimeReceived2 = timeReceived
			}
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		if gw == 1 {
			s.seenHashes[txHash] = &hashEntry{
				hash:            txHash,
				bxrTimeReceived: timeReceived,
			}
		} else {
			s.seenHashes[txHash] = &hashEntry{
				hash:             txHash,
				bxrTimeReceived2: timeReceived,
			}
		}

	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareService) processFeedFromEth(data *message) error {
	if data.err != nil {
		return fmt.Errorf(
			"failed to read message from ETH feed: %v", data.err)
	}

	timeReceived := time.Now()

	var msg ethTxFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	txHash := msg.Params.Result
	log.Debugf("got message at %s (ETH node, SUB), txHash: %s", timeReceived, txHash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if !s.excTxContents {
		go func() { s.hashes <- txHash }()
	} else if entry, ok := s.seenHashes[txHash]; ok {
		if entry.ethTimeReceived.IsZero() {
			entry.ethTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &hashEntry{
			ethTimeReceived: timeReceived,
			hash:            txHash,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareService) processTxContentsFromEth(data *message) error {
	txHash := data.hash

	if data.err != nil {
		return fmt.Errorf("cannot get transaction contents for hash %q: %v",
			txHash, data.err)
	}

	timeReceived := time.Now()

	var msg ethTxContentsResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	log.Debugf("got message at %s (ETH node, TXC), txHash: %s", timeReceived, txHash)

	if msg.Result == nil {
		return nil
	}

	if !s.addresses.Empty() && !s.addresses.Contains(msg.Result.To) {
		return nil
	}

	gasPrice, err := parseGasPrice(msg.Result.GasPrice)
	if err != nil {
		return fmt.Errorf("cannot parse gas price %q for transaction %q: %v",
			msg.Result.GasPrice, txHash, err)
	}

	if s.minGasPrice != nil && float64(gasPrice) < *s.minGasPrice {
		s.lowFeeHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.ethTimeReceived.IsZero() {
			entry.ethTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &hashEntry{
			ethTimeReceived: timeReceived,
			hash:            txHash,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareService) stats(ignoreDelta int, verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		txSeenByBothFeedsGatewayFirst       = 0
		txSeenByBothFeedsGateway2First      = 0
		txReceivedByGatewayFirstTotalDelta  = 0.0
		txReceivedByGateway2FirstTotalDelta = 0.0
		newTxFromGatewayFeedFirst           = 0
		newTxFromGateway2FeedFirst          = 0
		totalTxFromGateway                  = 0
		totalTxFromGateway2                 = 0
	)

	for txHash, entry := range s.seenHashes {
		if entry.bxrTimeReceived.IsZero() {
			ethNodeTimeReceived := entry.ethTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", ethNodeTimeReceived.Format(timestampFormat), "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromGateway2FeedFirst++
			totalTxFromGateway2++
			continue
		}
		if entry.bxrTimeReceived2.IsZero() {
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
			gatewayTimeReceived2 = entry.bxrTimeReceived2
			gatewayTimeReceived  = entry.bxrTimeReceived
			timeReceivedDiff     = gatewayTimeReceived.Sub(gatewayTimeReceived2)
		)

		totalTxFromGateway++
		totalTxFromGateway2++

		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			s.highDeltaHashes.Add(entry.hash)
			continue
		}

		if s.allHashesFile != nil {
			record := []string{
				txHash,
				gatewayTimeReceived.Format(timestampFormat),
				gatewayTimeReceived2.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Milliseconds()),
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(gatewayTimeReceived2):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Seconds()
		case gatewayTimeReceived2.Before(gatewayTimeReceived):
			newTxFromGateway2FeedFirst++
			txSeenByBothFeedsGateway2First++
			txReceivedByGateway2FirstTotalDelta += timeReceivedDiff.Seconds()
		}
	}

	var (
		newTxSeenByBothFeeds = txSeenByBothFeedsGatewayFirst +
			txSeenByBothFeedsGateway2First
		txReceivedByGatewayFirstAvgDelta  = 0
		txReceivedByGateway2FirstAvgDelta = 0
		txPercentageSeenByGatewayFirst    = 0
	)

	if txSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = int(math.Round(
			txReceivedByGatewayFirstTotalDelta / float64(txSeenByBothFeedsGatewayFirst) * 1000))
	}

	if txSeenByBothFeedsGateway2First != 0 {
		txReceivedByGateway2FirstAvgDelta = int(math.Round(
			txReceivedByGateway2FirstTotalDelta / float64(txSeenByBothFeedsGateway2First) * 1000))
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = int(
			(float64(txSeenByBothFeedsGatewayFirst) / float64(newTxSeenByBothFeeds)) * 100)
	}

	results := fmt.Sprintf(
		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from Gateway Denys first: %d\n"+
			"Number of transactions received from Gateway Develop node first: %d\n"+
			"Percentage of transactions seen first from Gateway Denys: %d%%\n"+
			"Average time difference for transactions received first from Gateway Denys (ms): %d\n"+
			"Average time difference for transactions received first from Gateway Develop node (ms): %d\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from Gateway Denys: %d\n"+
			"Total tx from Gateway Develop: %d\n"+
			"Number of low fee tx ignored: %d\n",

		newTxSeenByBothFeeds,
		txSeenByBothFeedsGatewayFirst,
		txSeenByBothFeedsGateway2First,
		txPercentageSeenByGatewayFirst,
		txReceivedByGatewayFirstAvgDelta,
		txReceivedByGateway2FirstAvgDelta,
		totalTxFromGateway,
		totalTxFromGateway2,
		len(s.lowFeeHashes))

	verboseResults := fmt.Sprintf(
		"Number of high delta tx ignored: %d\n"+
			"Number of new transactions received first from gateway: %d\n"+
			"Number of new transactions received first from node: %d\n"+
			"Total number of transactions seen: %d\n",
		len(s.highDeltaHashes),
		newTxFromGatewayFeedFirst,
		newTxFromGateway2FeedFirst,
		newTxFromGateway2FeedFirst+newTxFromGatewayFeedFirst,
	)

	if verbose {
		results += verboseResults
	}

	return results
}

func (s *TxFeedsCompareService) readFeedFromBX(
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

func (s *TxFeedsCompareService) readFeedFromEth(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
) {
	defer wg.Done()

	log.Infof("Initiating connection to %s", uri)
	conn, err := ws.NewConnection(uri, "")
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

	sub, err := conn.SubscribeTxFeedEth(1)
	if err != nil {
		log.Errorf("cannot subscribe to ETH feed: %v", err)
		return
	}

	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from ETH feed: %v", err)
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

func (s *TxFeedsCompareService) readTxContentsFromEth(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
) {
	defer wg.Done()

	log.Infof("Initiating connection to %s", uri)
	conn, err := ws.NewConnection(uri, "")
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

	for {
		select {
		case <-ctx.Done():
			return
		case txHash, ok := <-s.hashes:
			if !ok {
				return
			}

			var (
				data, err = conn.Call(ws.NewRequest(1, "eth_getTransactionByHash", []interface{}{txHash}))
				msg       = &message{
					hash:  txHash,
					err:   err,
					bytes: data,
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

func (s *TxFeedsCompareService) clearTrailNewHashes() {
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

func (s *TxFeedsCompareService) drainChannels() {
	done := make(chan struct{})
	go func() {
		for len(s.hashes) > 0 {
			<-s.hashes
		}

		for len(s.ethTxCh) > 0 {
			<-s.ethTxCh
		}

		done <- struct{}{}
	}()
	<-done
}

func parseGasPrice(str string) (gasPrice int64, err error) {
	return strconv.ParseInt(str, 0, 64)
}
