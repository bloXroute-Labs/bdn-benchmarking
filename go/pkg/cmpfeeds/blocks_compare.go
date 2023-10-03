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
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type blocksFeed interface {
	Receive(ctx context.Context, wg *sync.WaitGroup, out chan *blocks.Message)
	ParseMessage(message *blocks.Message) (*blocks.Block, error)
	Name() string
}

type CompareBlocksService struct {
	handlers chan handler

	firstFeed  blocksFeed
	secondFeed blocksFeed

	firstFeedChan  chan *blocks.Message
	secondFeedChan chan *blocks.Message

	trailNewBlocks  utils.HashSet
	leadNewBlocks   utils.HashSet
	highDeltaBlocks utils.HashSet

	seenBlocks map[string]*blockEntry

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer

	timeToBeginComparison time.Time
	timeToEndComparison   time.Time

	numIntervals int
}

func NewCompareBlocksService() *CompareBlocksService {
	const bufSize = 10000

	return &CompareBlocksService{
		handlers:       make(chan handler),
		firstFeedChan:  make(chan *blocks.Message, bufSize),
		secondFeedChan: make(chan *blocks.Message, bufSize),

		highDeltaBlocks: utils.NewHashSet(),
		trailNewBlocks:  utils.NewHashSet(),
		leadNewBlocks:   utils.NewHashSet(),

		seenBlocks: map[string]*blockEntry{},
	}
}

func (cs *CompareBlocksService) feedBuilders(c *cli.Context, feedName, uri string, enableTLS bool) (blocksFeed, error) {
	switch feedName {
	case "GatewayWS":
		return blocks.NewGatewayWS(c, uri), nil
	case "GatewayGRPC":
		return blocks.NewGatewayGRPC(c, uri, enableTLS), nil
	case "Fiber":
		return blocks.NewFiber(c, uri), nil
	}

	return nil, fmt.Errorf("feed: %s is not supported", feedName)
}

func (cs *CompareBlocksService) Run(c *cli.Context) error {
	var err error
	cs.firstFeed, err = cs.feedBuilders(c, c.String(flags.FirstFeed.Name), c.String(flags.FirstFeedURI.Name), c.Bool(flags.FirstFeedEnableTLS.Name))
	if err != nil {
		return err
	}

	cs.secondFeed, err = cs.feedBuilders(c, c.String(flags.SecondFeed.Name), c.String(flags.SecondFeedURI.Name), c.Bool(flags.SecondFeedEnableTLS.Name))
	if err != nil {
		return err
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
				"Hash",
				fmt.Sprintf("%s Time", firstFeedName),
				fmt.Sprintf("%s Time", secondFeedName),
				fmt.Sprintf("Time diff"),
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
		trailTimeSec = c.Int(flags.TrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	cs.timeToBeginComparison = time.Now().Add(time.Second * time.Duration(leadTimeSec))
	cs.timeToEndComparison = cs.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
	cs.numIntervals = c.Int(flags.NumIntervals.Name)

	readerGroup.Add(2)

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
						"%s\n",
					numIntervalsPassed,
					cs.numIntervals,
					intervalSec,
					time.Now().Format("2006-01-02T15:04:05.000"),
					cs.stats(c.Int(flags.IgnoreDelta.Name)),
				)

				cs.leadNewBlocks = utils.NewHashSet()
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

func (cs *CompareBlocksService) stats(ignoreDelta int) string {
	firstFeedName := cs.firstFeed.Name()
	secondFeedName := cs.secondFeed.Name()

	const timestampFormat = "2006-01-02T15:04:05.000000"

	var (
		blocksSeenByBothFeedsFirstFeedFirst       = float64(0)
		blocksSeenByBothFeedsSecondFeedFirst      = float64(0)
		blocksReceivedByFirstFeedFirstTotalDelta  = float64(0)
		blocksReceivedBySecondFeedFirstTotalDelta = float64(0)

		totalBlocksFromFirstFeed  = 0
		totalBlocksFromSecondFeed = 0

		timeDiffsUs = []int64{}
	)

	for hash, entry := range cs.seenBlocks {
		if !entry.firstFeedTimeReceived.IsZero() {
			totalBlocksFromFirstFeed++
		}
		if !entry.secondFeedTimeReceived.IsZero() {
			totalBlocksFromSecondFeed++
		}

		if entry.firstFeedTimeReceived.IsZero() {
			secondFeedTimeReceived := entry.secondFeedTimeReceived

			if cs.missingHashesFile != nil {
				line := fmt.Sprintf("%s \n", hash)
				if _, err := cs.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add hash %s: %v", hash, err)
				}
			}
			if cs.allHashesFile != nil {
				record := []string{hash, "0", secondFeedTimeReceived.Format(timestampFormat), "0"}
				if err := cs.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add hash %s,to all hashes file: %v", hash, err)
				}
			}
			continue
		}
		if entry.secondFeedTimeReceived.IsZero() {
			firstFeedTimeReceived := entry.firstFeedTimeReceived

			if cs.allHashesFile != nil {
				record := []string{hash, firstFeedTimeReceived.Format(timestampFormat), "0", "0"}
				if err := cs.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add hash %s,to all hashes file: %v", hash, err)
				}
			}
			continue
		}

		var (
			secondFeedTimeReceived = entry.secondFeedTimeReceived
			firstFeedTimeReceived  = entry.firstFeedTimeReceived
			timeReceivedDiff       = firstFeedTimeReceived.Sub(secondFeedTimeReceived)
		)

		timeDiffsUs = append(timeDiffsUs, timeReceivedDiff.Microseconds())
		if math.Abs(timeReceivedDiff.Seconds()) > float64(ignoreDelta) {
			cs.highDeltaBlocks.Add(hash)
			continue
		}

		if cs.allHashesFile != nil {
			record := []string{
				hash,
				firstFeedTimeReceived.Format(timestampFormat),
				secondFeedTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Microseconds()),
			}

			if err := cs.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add hash %s,to all hashes file: %v", hash, err)
			}
		}

		switch {
		case firstFeedTimeReceived.Before(secondFeedTimeReceived):
			blocksSeenByBothFeedsFirstFeedFirst++
			blocksReceivedByFirstFeedFirstTotalDelta += -timeReceivedDiff.Seconds()
		case secondFeedTimeReceived.Before(firstFeedTimeReceived):
			blocksSeenByBothFeedsSecondFeedFirst++
			blocksReceivedBySecondFeedFirstTotalDelta += timeReceivedDiff.Seconds()
		}
	}

	var (
		newBlocksSeenByBothFeeds = blocksSeenByBothFeedsFirstFeedFirst +
			blocksSeenByBothFeedsSecondFeedFirst
		blocksReceivedByFirstFeedFirstAvgDelta  = float64(0)
		blocksReceivedBySecondFeedFirstAvgDelta = float64(0)
		blocksPercentageSeenByGatewayFirst      = float64(0)
	)

	if blocksSeenByBothFeedsFirstFeedFirst != 0 {
		blocksReceivedByFirstFeedFirstAvgDelta = blocksReceivedByFirstFeedFirstTotalDelta / blocksSeenByBothFeedsFirstFeedFirst
	}

	if blocksSeenByBothFeedsSecondFeedFirst != 0 {
		blocksReceivedBySecondFeedFirstAvgDelta = blocksReceivedBySecondFeedFirstTotalDelta / blocksSeenByBothFeedsSecondFeedFirst
	}

	if newBlocksSeenByBothFeeds != 0 {
		blocksPercentageSeenByGatewayFirst = blocksSeenByBothFeedsFirstFeedFirst / newBlocksSeenByBothFeeds
	}

	var timeAverage = (blocksPercentageSeenByGatewayFirst*blocksReceivedByFirstFeedFirstAvgDelta - (1-blocksPercentageSeenByGatewayFirst)*blocksReceivedBySecondFeedFirstAvgDelta) * 1000

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
		"\nAnalysis of Blocks received on both feeds:\n"+
			"Number of blocks: %d\n"+
			"Number of blocks received from %s first: %d\n"+
			"Number of blocks received from %s first: %d\n"+
			"Percentage of blocks seen first from %s: %.2f%%\n"+
			"Average time difference for blocks received first from %s (ms): %f\n"+
			"Average time difference for blocks received first from %s (ms): %f\n"+
			"%s \n"+
			"Values with '-' is %s, values with '+' is %s \n"+
			"%s \n"+
			"%s \n"+
			"\nTotal Blocks summary:\n"+
			"Total blocks from %s: %d \n"+
			"Total blocks from %s: %d \n",

		int(newBlocksSeenByBothFeeds),

		firstFeedName,
		int(blocksSeenByBothFeedsFirstFeedFirst),
		secondFeedName,
		int(blocksSeenByBothFeedsSecondFeedFirst),

		firstFeedName,
		blocksPercentageSeenByGatewayFirst*100,

		firstFeedName,
		blocksReceivedByFirstFeedFirstAvgDelta*1000,
		secondFeedName,
		blocksReceivedBySecondFeedFirstAvgDelta*1000,

		winnerResult,

		firstFeedName,
		secondFeedName,
		calculatePercentiles(timeDiffsUs),
		makeHistogram(timeDiffsMs),

		firstFeedName,
		totalBlocksFromFirstFeed,
		secondFeedName,
		totalBlocksFromSecondFeed,
	)

	return results
}

func (cs *CompareBlocksService) handleUpdates(
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
			block, err := cs.firstFeed.ParseMessage(data)
			if err != nil {
				log.Errorf("error: %v", err)
				continue
			}

			hash := block.Hash

			if timeReceived.Before(cs.timeToBeginComparison) {
				cs.leadNewBlocks.Add(hash)
				continue
			}

			if entry, ok := cs.seenBlocks[hash]; ok {
				if entry.firstFeedTimeReceived.IsZero() {
					entry.firstFeedTimeReceived = timeReceived
				}
			} else if timeReceived.Before(cs.timeToEndComparison) &&
				!cs.trailNewBlocks.Contains(hash) &&
				!cs.leadNewBlocks.Contains(hash) {

				cs.seenBlocks[hash] = &blockEntry{
					firstFeedTimeReceived: timeReceived,
				}
			} else {
				cs.trailNewBlocks.Add(hash)
			}

		case data, ok := <-cs.secondFeedChan:
			if !ok {
				continue
			}

			timeReceived := data.FeedReceivedTime
			block, err := cs.secondFeed.ParseMessage(data)
			if err != nil {
				log.Errorf("error: %v", err)
				continue
			}

			hash := block.Hash

			if timeReceived.Before(cs.timeToBeginComparison) {
				cs.leadNewBlocks.Add(hash)
				continue
			}

			if entry, ok := cs.seenBlocks[hash]; ok {
				if entry.secondFeedTimeReceived.IsZero() {
					entry.secondFeedTimeReceived = timeReceived
				}
			} else if timeReceived.Before(cs.timeToEndComparison) &&
				!cs.trailNewBlocks.Contains(hash) &&
				!cs.leadNewBlocks.Contains(hash) {

				cs.seenBlocks[hash] = &blockEntry{
					secondFeedTimeReceived: timeReceived,
				}
			} else {
				cs.trailNewBlocks.Add(hash)
			}

		}
	}
}

func (cs *CompareBlocksService) clearTrailNewHashes() {
	done := make(chan struct{})
	go func() {
		cs.handlers <- func() error {
			cs.trailNewBlocks = utils.NewHashSet()
			done <- struct{}{}
			return nil
		}
	}()
	<-done
}

func (cs *CompareBlocksService) drainChannels() {
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
