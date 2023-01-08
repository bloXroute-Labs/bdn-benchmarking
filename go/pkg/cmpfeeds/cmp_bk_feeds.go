package cmpfeeds

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

// BkFeedsCompareService represents a service which compares block feeds time difference
// between ETH node and BX gateway.
type BkFeedsCompareService struct {
	handlers chan handler
	bxCh     chan *message
	bx2Ch    chan *message

	hashes chan string

	trailNewHashes        utils.HashSet
	leadNewHashes         utils.HashSet
	seenHashes            map[string]*hashEntry
	timeToBeginComparison time.Time
	timeToEndComparison   time.Time
	numIntervals          int

	excBkContents bool

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer
}

// NewBkFeedsCompareService creates and initializes BkFeedsCompareService instance.
func NewBkFeedsCompareService() *BkFeedsCompareService {
	const bufSize = 8192
	return &BkFeedsCompareService{
		handlers:       make(chan handler),
		bxCh:           make(chan *message),
		bx2Ch:          make(chan *message),
		hashes:         make(chan string, bufSize),
		trailNewHashes: utils.NewHashSet(),
		leadNewHashes:  utils.NewHashSet(),
		seenHashes:     make(map[string]*hashEntry),
	}
}

// Run is an entry point to the BkFeedsCompareService.
func (s *BkFeedsCompareService) Run(c *cli.Context) error {
	s.excBkContents = c.Bool(flags.ExcludeBkContents.Name)

	if d := strings.ToUpper(c.String(flags.Dump.Name)); d != "" {
		const all, missing, allAndMissing = "ALL", "MISSING", "ALL,MISSING"
		if d != all && d != missing && d != allAndMissing {
			return fmt.Errorf(
				"error: possible values for --%s are %q, %q, %q",
				flags.Dump.Name, all, missing, allAndMissing)
		}

		if strings.Contains(d, all) {
			const fileName = "all_block_hashes.csv"
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
				"BkHash", "BloXRoute Time", "Eth Time",
			}); err != nil {
				return fmt.Errorf("cannot write CSV header of file %q: %v", fileName, err)
			}
		}

		if strings.Contains(d, missing) {
			const fileName = "missing_block_hashes.txt"
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
		trailTimeSec = c.Int(flags.BkTrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	s.timeToBeginComparison = time.Now().Add(time.Second * time.Duration(leadTimeSec))
	s.timeToEndComparison = s.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
	s.numIntervals = c.Int(flags.NumIntervals.Name)

	var bxURI string
	if c.Bool(flags.UseCloudAPI.Name) {
		bxURI = c.String(flags.CloudAPIWSURI.Name)
	} else {
		bxURI = c.String(flags.Gateway.Name)
	}

	bx2URI := c.String(flags.Gateway2.Name)

	readerGroup.Add(2)
	go s.readFeedFromBX(
		ctx,
		&readerGroup,
		s.bxCh,
		bxURI,
		c.String(flags.AuthHeader.Name),
		c.String(flags.BkFeedName.Name),
	)
	go s.readFeedFromBX(
		ctx,
		&readerGroup,
		s.bx2Ch,
		bx2URI,
		c.String(flags.AuthHeader.Name),
		c.String(flags.BkFeed2Name.Name),
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
						"%s\n",
					numIntervalsPassed,
					s.numIntervals,
					intervalSec,
					time.Now().Format("2006-01-02T15:04:05.000"),
					s.stats(c.Int(flags.BkIgnoreDelta.Name)),
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

func (s *BkFeedsCompareService) handleUpdates(
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

			if err := s.processFeedFromBX(data, true); err != nil {
				log.Errorf("error: %v", err)
			}
		case data, ok := <-s.bx2Ch:
			if !ok {
				continue
			}

			if err := s.processFeedFromBX(data, false); err != nil {
				log.Errorf("error: %v", err)
			}
		}
	}
}

func (s *BkFeedsCompareService) processFeedFromBX(data *message, first bool) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed: %v", data.err)
	}

	timeReceived := time.Now()

	var msg bxBkFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	hash := msg.Params.Result.Hash
	if msg.Params.Result.Block.Body.ExecutionPayload.Hash != "" {
		hash = msg.Params.Result.Block.Body.ExecutionPayload.Hash
	}
	log.Debugf("got message at %s (BXR node first=%t, ALL), hash: %s", timeReceived, first, hash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(hash)
		return nil
	}

	if entry, ok := s.seenHashes[hash]; ok {
		if first {
			entry.bxrTimeReceived = timeReceived
		} else {
			entry.bxr2TimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(hash) &&
		!s.leadNewHashes.Contains(hash) {

		hashEntry := &hashEntry{
			hash: hash,
		}

		if first {
			hashEntry.bxrTimeReceived = timeReceived
		} else {
			hashEntry.bxr2TimeReceived = timeReceived
		}

		s.seenHashes[hash] = hashEntry
	}

	return nil
}

func (s *BkFeedsCompareService) processFeedFromEth(data *message) error {
	if data.err != nil {
		return fmt.Errorf(
			"failed to read message from ETH feed: %v", data.err)
	}

	timeReceived := time.Now()

	var msg ethBkFeedResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	hash := msg.Params.Result.Hash
	log.Debugf("got message at %s (ETH node, SUB), hash: %s", timeReceived, hash)

	if timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(hash)
		return nil
	}

	if !s.excBkContents {
		go func() { s.hashes <- hash }()
	} else if entry, ok := s.seenHashes[hash]; ok {
		if entry.ethTimeReceived.IsZero() {
			entry.ethTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(hash) &&
		!s.leadNewHashes.Contains(hash) {

		s.seenHashes[hash] = &hashEntry{
			ethTimeReceived: timeReceived,
			hash:            hash,
		}
	} else {
		s.trailNewHashes.Add(hash)
	}

	return nil
}

func (s *BkFeedsCompareService) processBkContentsFromEth(data *message) error {
	hash := data.hash

	if data.err != nil {
		return fmt.Errorf("cannot get block contents for hash %q: %v",
			hash, data.err)
	}

	timeReceived := time.Now()

	var msg ethBkContentsResponse
	if err := json.Unmarshal(data.bytes, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	log.Debugf("got message at %s (ETH node, BKC), hash: %s", timeReceived, hash)

	if msg.Result == nil {
		return nil
	}

	if entry, ok := s.seenHashes[hash]; ok {
		if entry.ethTimeReceived.IsZero() {
			entry.ethTimeReceived = timeReceived
		}
	} else if timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(hash) &&
		!s.leadNewHashes.Contains(hash) {

		s.seenHashes[hash] = &hashEntry{
			ethTimeReceived: timeReceived,
			hash:            hash,
		}
	} else {
		s.trailNewHashes.Add(hash)
	}

	return nil
}

func (s *BkFeedsCompareService) stats(ignoreDelta int) string {
	const timestampFormat = "2006-01-02T15:04:05.000"
	var (
		bkSeenByBothFeedsGatewayFirst       = 0
		bkSeenByBothFeedsGateway2First      = 0
		bkReceivedByGatewayFirstTotalDelta  = time.Duration(0)
		bkReceivedByGateway2FirstTotalDelta = time.Duration(0)
		newBkFromGatewayFeedFirst           = 0
		newBkFromGateway2FeedFirst          = 0
		totalBkFromGateway                  = 0
		totalBkFromGateway2                 = 0
	)

	for bkHash, entry := range s.seenHashes {
		if entry.bxrTimeReceived.IsZero() {
			gateway2TimeReceived := entry.bxr2TimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", bkHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add bkHash %q to missing hashes file: %v", bkHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{bkHash, "0", gateway2TimeReceived.Format(timestampFormat)}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add bkHash %q to all hashes file: %v", bkHash, err)
				}
			}
			newBkFromGateway2FeedFirst++
			totalBkFromGateway2++
			continue
		}
		if entry.bxr2TimeReceived.IsZero() {
			gatewayTimeReceived := entry.bxrTimeReceived

			if s.allHashesFile != nil {
				record := []string{bkHash, gatewayTimeReceived.Format(timestampFormat), "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add bkHash %q to all hashes file: %v", bkHash, err)
				}
			}
			newBkFromGatewayFeedFirst++
			totalBkFromGateway++
			continue
		}

		var (
			gateway2TimeReceived = entry.bxr2TimeReceived
			gatewayTimeReceived  = entry.bxrTimeReceived
			timeReceivedDiff     = gatewayTimeReceived.Sub(gateway2TimeReceived)
		)

		totalBkFromGateway++
		totalBkFromGateway2++

		if s.allHashesFile != nil {
			record := []string{
				bkHash,
				gatewayTimeReceived.Format(timestampFormat),
				gateway2TimeReceived.Format(timestampFormat),
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add bkHash %q to all hashes file: %v", bkHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(gateway2TimeReceived):
			newBkFromGatewayFeedFirst++
			bkSeenByBothFeedsGatewayFirst++
			bkReceivedByGatewayFirstTotalDelta += -timeReceivedDiff
		case gateway2TimeReceived.Before(gatewayTimeReceived):
			newBkFromGateway2FeedFirst++
			bkSeenByBothFeedsGateway2First++
			bkReceivedByGateway2FirstTotalDelta += timeReceivedDiff
		}
	}

	var (
		newBkSeenByBothFeeds = bkSeenByBothFeedsGatewayFirst +
			bkSeenByBothFeedsGateway2First
		bkReceivedByGatewayFirstAvgDelta  = time.Duration(0)
		bkReceivedByGateway2FirstAvgDelta = time.Duration(0)
		bkPercentageSeenByGatewayFirst    = 0
	)

	if bkSeenByBothFeedsGatewayFirst != 0 {
		bkReceivedByGatewayFirstAvgDelta =
			bkReceivedByGatewayFirstTotalDelta / time.Duration(bkSeenByBothFeedsGatewayFirst)
	}

	if bkSeenByBothFeedsGateway2First != 0 {
		bkReceivedByGateway2FirstAvgDelta =
			bkReceivedByGateway2FirstTotalDelta / time.Duration(bkSeenByBothFeedsGateway2First)
	}

	if newBkSeenByBothFeeds != 0 {
		bkPercentageSeenByGatewayFirst = int(
			(float64(bkSeenByBothFeedsGatewayFirst) / float64(newBkSeenByBothFeeds)) * 100)
	}

	return fmt.Sprintf("\nBlock summary\n"+
		"Number of new blocks received first from gateway: %d\n"+
		"Number of new blocks received first from gateway2: %d\n"+
		"Total number of blocks seen: %d\n"+
		"Total blocks from gateway: %d\n"+
		"Total blocks from gateway2: %d\n"+
		"\nAnalysis of Blocks received on both feeds:\n"+
		"Number of blocks: %d\n"+
		"Number of blocks received from Gateway first: %d\n"+
		"Number of blocks received from Gateway2 first: %d\n"+
		"Percentage of blocks seen first from gateway: %d\n"+
		"Average time difference for blocks received first from gateway: %s\n"+
		"Average time difference for blocks received first from gateway2: %s\n",
		newBkFromGatewayFeedFirst,
		newBkFromGateway2FeedFirst,
		newBkFromGatewayFeedFirst+newBkFromGateway2FeedFirst,
		totalBkFromGateway,
		totalBkFromGateway2,
		newBkSeenByBothFeeds,
		bkSeenByBothFeedsGatewayFirst,
		bkSeenByBothFeedsGateway2First,
		bkPercentageSeenByGatewayFirst,
		bkReceivedByGatewayFirstAvgDelta,
		bkReceivedByGateway2FirstAvgDelta,
	)
}

func (s *BkFeedsCompareService) readFeedFromBX(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
	authHeader string,
	feedName string,
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

	sub, err := conn.SubscribeBkFeedBX(1, feedName, s.excBkContents)

	if err != nil {
		log.Errorf("cannot subscribe to feed %q: %v", feedName, err)
		return
	}

	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from feed %q: %v", feedName, err)
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

func (s *BkFeedsCompareService) readFeedFromEth(
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

	sub, err := conn.SubscribeBkFeedEth(1)
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

func (s *BkFeedsCompareService) readBkContentsFromEth(
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
		case bkHash, ok := <-s.hashes:
			if !ok {
				return
			}

			var (
				data, err = conn.Call(ws.NewRequest(1, "eth_getBlockByHash", []interface{}{bkHash, true}))
				msg       = &message{
					hash:  bkHash,
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

func (s *BkFeedsCompareService) clearTrailNewHashes() {
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

func (s *BkFeedsCompareService) drainChannels() {
	done := make(chan struct{})
	go func() {
		for len(s.hashes) > 0 {
			<-s.hashes
		}

		done <- struct{}{}
	}()
	<-done
}
