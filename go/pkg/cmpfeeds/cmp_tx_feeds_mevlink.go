package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	mlstreamer "github.com/mevlink/streamer-go"
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

var mevLinkCh chan *message

// TxFeedsCompareMEVLinkService represents a service which compares transaction feeds time difference
// between mevlink gateway and BX cloud services.
type TxFeedsCompareMEVLinkService struct {
	handlers  chan handler
	mevLinkCh chan *message
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

// NewTxFeedsCompareMEVLinkService creates and initializes TxFeedsCompareMEVLinkService instance.
func NewTxFeedsCompareMEVLinkService() *TxFeedsCompareMEVLinkService {
	const bufSize = 8192
	return &TxFeedsCompareMEVLinkService{
		handlers:        make(chan handler),
		bxCh:            make(chan *message),
		mevLinkCh:       make(chan *message),
		bxBlockCh:       make(chan *message),
		hashes:          make(chan string, bufSize),
		lowFeeHashes:    utils.NewHashSet(),
		highDeltaHashes: utils.NewHashSet(),
		trailNewHashes:  utils.NewHashSet(),
		leadNewHashes:   utils.NewHashSet(),
		seenHashes:      make(map[string]*hashEntry),
	}
}

// Run is an entry point to the TxFeedsCompareMEVLinkService.
func (s *TxFeedsCompareMEVLinkService) Run(c *cli.Context) error {
	mevLinkCh = s.mevLinkCh
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
				"TxHash", "BloXRoute Time", "MEVLink Time", "Time Diff", "Mined In The Block",
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

	go s.readFeedFromMEVLink(
		ctx,
		s.mevLinkCh,
		c.String(flags.MEVLinkAPIKey.Name),
		c.String(flags.MEVLinkAPISecret.Name),
		c.Int(flags.NetworkNumber.Name),
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

func (s *TxFeedsCompareMEVLinkService) handleUpdates(
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
		case data, ok := <-s.bxCh:
			if !ok {
				continue
			}

			if err := s.processFeedFromBX(data); err != nil {
				log.Errorf("error: %v", err)
			}
		case data, ok := <-s.mevLinkCh:
			if !ok {
				continue
			}

			if err := s.processFeedFromMEVLink(data); err != nil {
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

func (s *TxFeedsCompareMEVLinkService) processFeedFromBX(data *message) error {
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

// DecodeHex gets the bytes of a hexadecimal string, with or without its `0x` prefix
func DecodeHex(str string) ([]byte, error) {
	if len(str) > 2 && str[:2] == "0x" {
		str = str[2:]
	}
	return hex.DecodeString(str)
}

func (s *TxFeedsCompareMEVLinkService) processFeedFromMEVLink(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := data.timeReceived

	txStr := hexutil.Encode(data.bytes)
	txBytes, err := DecodeHex(txStr)
	if err != nil {
		return err
	}

	var ethTx types.Transaction
	err = ethTx.UnmarshalBinary(txBytes)
	if err != nil {
		// If UnmarshalBinary failed, we will try RLP in case user made mistake
		e := rlp.DecodeBytes(txBytes, &ethTx)
		if e != nil {
			log.Errorf("could not decode Ethereum transaction: %v", err)
			return e
		}
	}
	txHash := ethTx.Hash().String()
	log.Debugf("got message at %s (mev link, ALL), txHash: %s", timeReceived, txHash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(txHash)
		return nil
	}

	if entry, ok := s.seenHashes[txHash]; ok {
		if entry.mevLinkTimeReceived.IsZero() {
			entry.mevLinkTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(txHash) &&
		!s.leadNewHashes.Contains(txHash) {

		s.seenHashes[txHash] = &hashEntry{
			hash:                txHash,
			mevLinkTimeReceived: timeReceived,
		}
	} else {
		s.trailNewHashes.Add(txHash)
	}

	return nil
}

func (s *TxFeedsCompareMEVLinkService) stats(ignoreDelta int, verbose bool) string {
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
				log.Debugf("mevLink transaction %v was not found in a block\n", txHash)
			} else if entry.mevLinkTimeReceived.IsZero() {
				log.Debugf("bloxroute transaction %v was not found in a block\n", txHash)
			} else {
				log.Debugf("both mev link and bloxroute transaction %v was not found in a block\n", txHash)
			}
			continue
		}

		if entry.bxrTimeReceived.IsZero() {
			mevLinkTimeReceived := entry.mevLinkTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", txHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txHash %q to missing hashes file: %v", txHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{txHash, "0", mevLinkTimeReceived.Format(timestampFormat), "0", "1"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
				}
			}
			newTxFromMEVLinkFeedFirst++
			totalTxFromMEVLink++
			continue
		}
		if entry.mevLinkTimeReceived.IsZero() {
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
			mevLinkTimeReceived = entry.mevLinkTimeReceived
			gatewayTimeReceived = entry.bxrTimeReceived
			timeReceivedDiff    = gatewayTimeReceived.Sub(mevLinkTimeReceived)
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
				gatewayTimeReceived.Format(timestampFormat),
				mevLinkTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Milliseconds()),
				"1",
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %q to all hashes file: %v", txHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(mevLinkTimeReceived):
			newTxFromGatewayFeedFirst++
			txSeenByBothFeedsGatewayFirst++
			txReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Seconds()
		case mevLinkTimeReceived.Before(gatewayTimeReceived):
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
			"Number of transactions received from bloXroute gateway first: %d\n"+
			"Number of transactions received from MEVLink first: %d\n"+
			"Percentage of transactions seen first from bloXroute gateway: %.2f%%\n"+
			"Average time difference for transactions received first from bloXroute gateway (ms): %f\n"+
			"Average time difference for transactions received first from MEVLink (ms): %f\n"+
			"Gateway is faster then MEVLink by (ms): %.6f\n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from bloXroute gateway: %d\n"+
			"Total tx from MEVLink: %d\n",

		int(newTxSeenByBothFeeds),
		int(txSeenByBothFeedsGatewayFirst),
		int(txSeenByBothFeedsMEVLinkFirst),
		txPercentageSeenByGatewayFirst*100,
		txReceivedByGatewayFirstAvgDelta*1000,
		txReceivedByMevLinkFirstAvgDelta*1000,
		timeAverage*1000,
		totalTxFromGateway,
		totalTxFromMEVLink,
	)

	verboseResults := fmt.Sprintf(
		"Number of high delta tx ignored: %d\n"+
			"Number of new transactions received first from gateway: %d\n"+
			"Number of new transactions received first from node: %d\n"+
			"Total number of transactions seen: %d\n",
		len(s.highDeltaHashes),
		newTxFromGatewayFeedFirst,
		newTxFromMEVLinkFeedFirst,
		newTxFromMEVLinkFeedFirst+newTxFromGatewayFeedFirst,
	)

	if verbose {
		results += verboseResults
	}

	return results
}

func (s *TxFeedsCompareMEVLinkService) readFeedFromBX(
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
			log.Info("Stop gateway feed")
			return
		case out <- msg:
		}
	}
}

func helper(txb []byte, hash mlstreamer.NullableHash, noticed, propagated time.Time) {
	//Getting the transaction hash and printing the relevant times
	//var hasher = sha3.NewLegacyKeccak256()
	//hasher.Write(txb)
	//var tx_hash = hasher.Sum(nil)

	//hashStr := hex.EncodeToString(tx_hash)
	timeReceived := time.Now()

	msg := &message{
		bytes:        txb,
		err:          nil,
		timeReceived: timeReceived,
	}
	mevLinkCh <- msg
	//log.Infof("Got tx '" + hashStr + "'! Was noticed on ", noticed, "and sent on", propagated)
}

func (s *TxFeedsCompareMEVLinkService) readFeedFromMEVLink(
	ctx context.Context,
	out chan<- *message,
	apiKey string,
	apiSecret string,
	networkNum int,
) {
	log.Info("Initiating connection to mev link")

	str := mlstreamer.NewStreamer(apiKey, apiSecret, mlstreamer.Network(networkNum))
	str.OnTransaction(helper)

	go func() {
		<-ctx.Done()
		log.Info("Stop mevlink feed")
		str.Stop()
	}()

	if err := str.Stream(); err != nil {
		log.Error(err)
	}
}

func (s *TxFeedsCompareMEVLinkService) clearTrailNewHashes() {
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

func (s *TxFeedsCompareMEVLinkService) readBlockFeedFromBX(
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

func (s *TxFeedsCompareMEVLinkService) processBlockFeedFromBX(data *message) error {
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
