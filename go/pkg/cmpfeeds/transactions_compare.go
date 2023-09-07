package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/pkg/cmpfeeds/feeds/blocks"
	"performance/pkg/cmpfeeds/feeds/transactions"
	"performance/pkg/constant"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type transactionFeed interface {
	Receive(ctx context.Context, wg *sync.WaitGroup, out chan *transactions.Message)
	ParseMessage(message *transactions.Message) (*transactions.Transaction, error)
	Name() string
}

type senderWithNonceFeed interface {
	Receive(ctx context.Context, wg *sync.WaitGroup, out chan *blocks.Message)
	ParseMessageToSenderWithNonce(message *blocks.Message) ([]string, error)
	Name() string
}

type CompareTransactionsService struct {
	handlers chan handler

	firstFeed   transactionFeed
	secondFeed  transactionFeed
	bxBlockFeed senderWithNonceFeed

	firstFeedChan  chan *transactions.Message
	secondFeedChan chan *transactions.Message
	bxBlockCh      chan *blocks.Message

	// sender+nonce to txInfo
	seenTXs map[string]*nonceSenderEntry

	seenSenderAndNonceInBlock utils.HashSet
	trailNewTXs               utils.HashSet
	leadNewTXs                utils.HashSet
	highDeltaTXs              utils.HashSet

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer

	timeToBeginComparison time.Time
	timeToEndComparison   time.Time

	numIntervals int
}

func NewCompareTransactionsService() *CompareTransactionsService {
	const bufSize = 10000

	return &CompareTransactionsService{
		handlers:       make(chan handler),
		firstFeedChan:  make(chan *transactions.Message, bufSize),
		secondFeedChan: make(chan *transactions.Message, bufSize),
		bxBlockCh:      make(chan *blocks.Message, bufSize),

		highDeltaTXs:              utils.NewHashSet(),
		trailNewTXs:               utils.NewHashSet(),
		leadNewTXs:                utils.NewHashSet(),
		seenSenderAndNonceInBlock: utils.NewHashSet(),

		seenTXs: make(map[string]*nonceSenderEntry, constant.MapSize),
	}
}

func (cs *CompareTransactionsService) feedBuilders(c *cli.Context, feedName, uri string) (transactionFeed, error) {
	switch feedName {
	case "Mevlink":
		return transactions.NewMevlink(c), nil
	case "GatewayWS":
		return transactions.NewGatewayWS(c, uri), nil
	case "GatewayGRPC":
		return transactions.NewGatewayGRPC(c, uri), nil
	case "Fiber":
		return transactions.NewFiber(c, uri), nil
	}

	return nil, fmt.Errorf("feed: %s is not supported", feedName)
}

func (cs *CompareTransactionsService) Run(c *cli.Context) error {
	var err error
	cs.firstFeed, err = cs.feedBuilders(c, c.String(flags.FirstFeed.Name), c.String(flags.FirstFeedURI.Name))
	if err != nil {
		return err
	}

	cs.secondFeed, err = cs.feedBuilders(c, c.String(flags.SecondFeed.Name), c.String(flags.SecondFeedURI.Name))
	if err != nil {
		return err
	}

	cs.bxBlockFeed = blocks.NewGatewayWS(c, c.String(flags.BlockFeedURI.Name))

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
				if cs.allHashesFile != nil {
					cs.allHashesFile.Flush()
				}
				if err := file.Sync(); err != nil {
					log.Errorf("cannot sync contents of file %q: %v", fileName, err)
				}
				if err := file.Close(); err != nil {
					log.Errorf("cannot close file %q: %v", fileName, err)
				}
			}()

			cs.allHashesFile = csv.NewWriter(file)

			firstFeedName := cs.firstFeed.Name()
			secondFeedName := cs.secondFeed.Name()

			if err := cs.allHashesFile.Write([]string{
				"Nonce",
				"Sender",
				fmt.Sprintf("%s Hash", firstFeedName),
				fmt.Sprintf("%s Hash", secondFeedName),
				fmt.Sprintf("%s Time", firstFeedName),
				fmt.Sprintf("%s Time", secondFeedName),
				fmt.Sprintf("Time diff"),
				fmt.Sprintf("%s message len", firstFeedName),
				fmt.Sprintf("%s message len", secondFeedName),
				"Hashes are equal",
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

			cs.missingHashesFile = bufio.NewWriter(file)

			defer func() {
				if cs.missingHashesFile != nil {
					if err := cs.missingHashesFile.Flush(); err != nil {
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

	cs.timeToBeginComparison = time.Now().Add(time.Second * time.Duration(leadTimeSec))
	cs.timeToEndComparison = cs.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
	cs.numIntervals = c.Int(flags.NumIntervals.Name)

	readerGroup.Add(3)

	go cs.firstFeed.Receive(
		ctx,
		&readerGroup,
		cs.firstFeedChan,
	)

	go cs.secondFeed.Receive(
		ctx,
		&readerGroup,
		cs.secondFeedChan,
	)

	go cs.bxBlockFeed.Receive(
		ctx,
		&readerGroup,
		cs.bxBlockCh,
	)

	handleGroup.Add(1)
	go cs.handleUpdates(ctx, &handleGroup)

	time.Sleep(time.Second * time.Duration(leadTimeSec))
	for i := 0; i < cs.numIntervals; i++ {
		time.Sleep(time.Second * time.Duration(intervalSec))
		cs.clearTrailNewHashes()
		time.Sleep(time.Second * time.Duration(trailTimeSec))

		func(numIntervalsPassed int) {
			cs.handlers <- func() error {
				msg := fmt.Sprintf(
					"-----------------------------------------------------\n"+
						"Interval (%d/%d): %d seconds. \n"+
						"End time: %s \n"+
						"Minimum gas price: %f \n"+
						"%s\n",
					numIntervalsPassed,
					cs.numIntervals,
					intervalSec,
					time.Now().Format("2006-01-02T15:04:05.000"),
					c.Float64(flags.MinGasPrice.Name),
					cs.stats(c.Int(flags.TxIgnoreDelta.Name)),
				)

				cs.seenTXs = make(map[string]*nonceSenderEntry)
				cs.leadNewTXs = utils.NewHashSet()
				cs.timeToEndComparison = time.Now().Add(time.Second * time.Duration(intervalSec))

				fmt.Print(msg)

				if numIntervalsPassed == cs.numIntervals {
					fmt.Printf("%d of %d intervals complete. Exiting.\n\n",
						numIntervalsPassed, cs.numIntervals)
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

func (cs *CompareTransactionsService) stats(ignoreDelta int) string {
	firstFeedName := cs.firstFeed.Name()
	secondFeedName := cs.secondFeed.Name()

	const timestampFormat = "2006-01-02T15:04:05.000000"

	var (
		txSeenByBothFeedsFirstFeedFirst       = float64(0)
		txSeenByBothFeedsSecondFeedFirst      = float64(0)
		txReceivedByFirstFeedFirstTotalDelta  = float64(0)
		txReceivedBySecondFeedFirstTotalDelta = float64(0)

		newTxFromFirstFeedFirst         = 0
		newTxFromSecondFeedFirst        = 0
		totalTxInBlockFromFirstFeed     = 0
		totalTxFromFirstFeed            = 0
		totalTxInBlockFromSecondFeed    = 0
		totalTxFromSecondFeed           = 0
		totalBytesFromFirstFeed         = 0
		totalBytesFromSecondFeed        = 0
		totalBytesInBlockFromFirstFeed  = 0
		totalBytesInBlockFromSecondFeed = 0
		numberOfDifferentHashes         = 0
		timeDiffsUs                     = []int64{}
	)

	for txKey, entry := range cs.seenTXs {
		if !entry.firstFeedTimeReceived.IsZero() {
			totalTxFromFirstFeed++
			totalBytesFromFirstFeed += entry.firstFeedMessageSize
		}
		if !entry.secondFeedTimeReceived.IsZero() {
			totalTxFromSecondFeed++
			totalBytesFromSecondFeed += entry.secondFeedMessageSize
		}

		if !cs.seenSenderAndNonceInBlock.Contains(txKey) {
			if entry.firstFeedTimeReceived.IsZero() {
				log.Debugf("%s transaction %v was not found in a block\n", firstFeedName, txKey)
			} else if entry.secondFeedTimeReceived.IsZero() {
				log.Debugf("%s transaction %v was not found in a block\n", secondFeedName, txKey)
			} else {
				log.Debugf("both feeds: %s and %s transaction %v was not found in a block\n", firstFeedName, secondFeedName, txKey)
			}
			continue
		}

		totalBytesInBlockFromFirstFeed += entry.firstFeedMessageSize
		totalBytesInBlockFromSecondFeed += entry.secondFeedMessageSize

		if entry.firstFeedTimeReceived.IsZero() {
			secondFeedTimeReceived := entry.secondFeedTimeReceived

			if cs.missingHashesFile != nil {
				line := fmt.Sprintf("%d %s \n", entry.nonce, entry.sender)
				if _, err := cs.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add txNonce %d, sender: %s to missing hashes file: %v", entry.nonce, entry.sender, err)
				}
			}
			if cs.allHashesFile != nil {
				record := []string{strconv.FormatUint(entry.nonce, 10), entry.sender, "", entry.secondFeedTXHash, "0", secondFeedTimeReceived.Format(timestampFormat), "0", "0", strconv.Itoa(entry.secondFeedMessageSize), ""}
				if err := cs.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txNonce %d, sender: %s to all hashes file: %v", entry.nonce, entry.sender, err)
				}
			}
			newTxFromSecondFeedFirst++
			totalTxInBlockFromSecondFeed++
			continue
		}
		if entry.secondFeedTimeReceived.IsZero() {
			firstFeedTimeReceived := entry.firstFeedTimeReceived

			if cs.allHashesFile != nil {
				record := []string{strconv.FormatUint(entry.nonce, 10), entry.sender, entry.firstFeedTXHash, "", firstFeedTimeReceived.Format(timestampFormat), "0", "0", strconv.Itoa(entry.firstFeedMessageSize), "0", ""}
				if err := cs.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txNonce %d, sender: %s to all hashes file: %v", entry.nonce, entry.sender, err)
				}
			}
			newTxFromFirstFeedFirst++
			totalTxInBlockFromFirstFeed++
			continue
		}

		var (
			secondFeedTimeReceived = entry.secondFeedTimeReceived
			firstFeedTimeReceived  = entry.firstFeedTimeReceived
			timeReceivedDiff       = firstFeedTimeReceived.Sub(secondFeedTimeReceived)
		)

		timeDiffsUs = append(timeDiffsUs, timeReceivedDiff.Microseconds())

		totalTxInBlockFromFirstFeed++
		totalTxInBlockFromSecondFeed++

		if entry.firstFeedTXHash != entry.secondFeedTXHash {
			numberOfDifferentHashes++
		}

		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			cs.highDeltaTXs.Add(txKey)
			continue
		}

		if cs.allHashesFile != nil {
			record := []string{
				strconv.FormatUint(entry.nonce, 10),
				entry.sender,
				entry.firstFeedTXHash,
				entry.secondFeedTXHash,
				firstFeedTimeReceived.Format(timestampFormat),
				secondFeedTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Microseconds()),
				strconv.Itoa(entry.firstFeedMessageSize),
				strconv.Itoa(entry.secondFeedMessageSize),
				strconv.FormatBool(entry.firstFeedTXHash == entry.secondFeedTXHash),
			}

			if err := cs.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add txHash %d, sender: %s to all hashes file: %v", entry.nonce, entry.sender, err)
			}
		}

		switch {
		case firstFeedTimeReceived.Before(secondFeedTimeReceived):
			newTxFromFirstFeedFirst++
			txSeenByBothFeedsFirstFeedFirst++
			txReceivedByFirstFeedFirstTotalDelta += -timeReceivedDiff.Seconds()
		case secondFeedTimeReceived.Before(firstFeedTimeReceived):
			newTxFromSecondFeedFirst++
			txSeenByBothFeedsSecondFeedFirst++
			txReceivedBySecondFeedFirstTotalDelta += timeReceivedDiff.Seconds()
		}
	}

	var (
		newTxSeenByBothFeeds = txSeenByBothFeedsFirstFeedFirst +
			txSeenByBothFeedsSecondFeedFirst
		txReceivedByFirstFeedFirstAvgDelta  = float64(0)
		txReceivedBySecondFeedFirstAvgDelta = float64(0)
		txPercentageSeenByGatewayFirst      = float64(0)
	)

	if txSeenByBothFeedsFirstFeedFirst != 0 {
		txReceivedByFirstFeedFirstAvgDelta = txReceivedByFirstFeedFirstTotalDelta / txSeenByBothFeedsFirstFeedFirst
	}

	if txSeenByBothFeedsSecondFeedFirst != 0 {
		txReceivedBySecondFeedFirstAvgDelta = txReceivedBySecondFeedFirstTotalDelta / txSeenByBothFeedsSecondFeedFirst
	}

	if newTxSeenByBothFeeds != 0 {
		txPercentageSeenByGatewayFirst = txSeenByBothFeedsFirstFeedFirst / newTxSeenByBothFeeds
	}

	var timeAverage = (txPercentageSeenByGatewayFirst*txReceivedByFirstFeedFirstAvgDelta - (1-txPercentageSeenByGatewayFirst)*txReceivedBySecondFeedFirstAvgDelta) * 1000

	var winnerResult string

	if timeAverage < 0 {
		winnerResult = fmt.Sprintf("%s is faster then %s by (ms): %.6f", secondFeedName, firstFeedName, timeAverage*-1)
	} else {
		winnerResult = fmt.Sprintf("%s is faster then %s by (ms): %.6f", firstFeedName, secondFeedName, timeAverage)
	}

	timeDiffsMs := []float64{}
	for _, timestamp := range timeDiffsUs {
		timeDiffsMs = append(timeDiffsMs, float64(timestamp)/1000)
	}

	results := fmt.Sprintf(

		"\nAnalysis of Transactions received on both feeds:\n"+
			"Number of transactions: %d\n"+
			"Number of transactions received from %s first: %d\n"+
			"Number of transactions received from %s first: %d\n"+
			"Percentage of transactions seen first from %s: %.2f%%\n"+
			"Average time difference for transactions received first from %s (ms): %f\n"+
			"Average time difference for transactions received first from %s (ms): %f\n"+
			"%s \n"+
			"Values with '-' is %s, values with '+' is %s \n"+
			"%s \n"+
			"%s \n"+
			"\nTotal Transactions summary:\n"+
			"Total tx from %s: %d (in block) / %d (total) \n"+
			"Total tx from %s: %d (in block) / %d (total) \n"+
			"Total bytes from %s: %d (in block) / %d (total) \n"+
			"Total bytes from %s: %d (in block) / %d (total) \n"+
			"Number of different hashes: %d",

		int(newTxSeenByBothFeeds),

		firstFeedName,
		int(txSeenByBothFeedsFirstFeedFirst),
		secondFeedName,
		int(txSeenByBothFeedsSecondFeedFirst),

		firstFeedName,
		txPercentageSeenByGatewayFirst*100,

		firstFeedName,
		txReceivedByFirstFeedFirstAvgDelta*1000,
		secondFeedName,
		txReceivedBySecondFeedFirstAvgDelta*1000,

		winnerResult,

		firstFeedName,
		secondFeedName,
		calculatePercentiles(timeDiffsUs),
		makeHistogram(timeDiffsMs),

		firstFeedName,
		totalTxInBlockFromFirstFeed,
		totalTxFromFirstFeed,
		secondFeedName,
		totalTxInBlockFromSecondFeed,
		totalTxFromSecondFeed,

		firstFeedName,
		totalBytesInBlockFromFirstFeed,
		totalBytesFromFirstFeed,
		secondFeedName,
		totalBytesInBlockFromSecondFeed,
		totalBytesFromSecondFeed,

		numberOfDifferentHashes,
	)

	return results
}

func (cs *CompareTransactionsService) handleUpdates(
	ctx context.Context,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-cs.handlers:
			if !ok {
				continue
			}

			if err := update(); err != nil {
				log.Errorf("error in update function: %v", err)
			}
		case data, ok := <-cs.firstFeedChan:
			if !ok {
				continue
			}

			timeReceived := data.FeedReceivedTime
			transaction, err := cs.firstFeed.ParseMessage(data)
			if err != nil {
				log.Errorf("error: %v", err)
				continue
			}
			txKey := transaction.Key()

			if timeReceived.Before(cs.timeToBeginComparison) {
				cs.leadNewTXs.Add(txKey)
				continue
			}

			if entry, ok := cs.seenTXs[txKey]; ok {
				if entry.firstFeedTimeReceived.IsZero() {
					entry.firstFeedTimeReceived = timeReceived
					entry.firstFeedMessageSize = data.Size
					entry.firstFeedTXHash = transaction.Hash
				}
			} else if timeReceived.Before(cs.timeToEndComparison) &&
				!cs.trailNewTXs.Contains(txKey) &&
				!cs.leadNewTXs.Contains(txKey) {

				cs.seenTXs[txKey] = &nonceSenderEntry{
					nonce:                 transaction.Nonce,
					sender:                transaction.Sender,
					firstFeedTimeReceived: timeReceived,
					firstFeedMessageSize:  data.Size,
					firstFeedTXHash:       transaction.Hash,
				}
			} else {
				cs.trailNewTXs.Add(txKey)
			}

		case data, ok := <-cs.secondFeedChan:
			if !ok {
				continue
			}

			timeReceived := data.FeedReceivedTime
			transaction, err := cs.secondFeed.ParseMessage(data)
			if err != nil {
				log.Errorf("error: %v", err)
				continue
			}
			txKey := transaction.Key()

			if timeReceived.Before(cs.timeToBeginComparison) {
				cs.leadNewTXs.Add(txKey)
				continue
			}

			if entry, ok := cs.seenTXs[txKey]; ok {
				if entry.secondFeedTimeReceived.IsZero() {
					entry.secondFeedTimeReceived = timeReceived
					entry.secondFeedMessageSize = data.Size
					entry.secondFeedTXHash = transaction.Hash
				}
			} else if timeReceived.Before(cs.timeToEndComparison) &&
				!cs.trailNewTXs.Contains(txKey) &&
				!cs.leadNewTXs.Contains(txKey) {

				cs.seenTXs[txKey] = &nonceSenderEntry{
					nonce:                  transaction.Nonce,
					sender:                 transaction.Sender,
					secondFeedTimeReceived: timeReceived,
					secondFeedMessageSize:  data.Size,
					secondFeedTXHash:       transaction.Hash,
				}
			} else {
				cs.trailNewTXs.Add(txKey)
			}

		case data, ok := <-cs.bxBlockCh:
			if !ok {
				fmt.Printf("failed to get data from bxblockch %v", data)
				continue
			}

			blockTxs, err := cs.bxBlockFeed.ParseMessageToSenderWithNonce(data)
			if err != nil {
				continue
			}

			for _, tx := range blockTxs {
				cs.seenSenderAndNonceInBlock.Add(tx)
			}
		}
	}
}

func (cs *CompareTransactionsService) clearTrailNewHashes() {
	done := make(chan struct{})
	go func() {
		cs.handlers <- func() error {
			cs.trailNewTXs = utils.NewHashSet()
			done <- struct{}{}
			return nil
		}
	}()
	<-done
}

func (cs *CompareTransactionsService) drainChannels() {
	done := make(chan struct{})
	go func() {
		for len(cs.firstFeedChan) > 0 {
			<-cs.firstFeedChan
		}

		for len(cs.secondFeedChan) > 0 {
			<-cs.secondFeedChan
		}

		done <- struct{}{}
	}()
	<-done
}
