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
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	pb "performance/pkg/cmpfeeds/protobuf"
	"strings"
	"sync"
	"time"
)

// BlockGrpcWSCompareService represents a service which compares block feeds time difference
// between Ws and gRPC connection.
type BlockGrpcWSCompareService struct {
	handlers chan handler
	bxCh     chan *message
	bxGrpcCh chan *message

	hashes chan string

	trailNewHashes        utils.HashSet
	leadNewHashes         utils.HashSet
	lowFeeHashes          utils.HashSet
	seenHashes            map[string]*grpcHashEntry
	timeToBeginComparison time.Time
	timeToEndComparison   time.Time
	numIntervals          int

	feedName string

	allHashesFile     *csv.Writer
	missingHashesFile *bufio.Writer
}

// NewBlockWsGrpcCompareService creates and initializes BlockGrpcWSCompareService instance.
func NewBlockWsGrpcCompareService() *BlockGrpcWSCompareService {
	const bufSize = 8192
	return &BlockGrpcWSCompareService{
		handlers:       make(chan handler),
		bxCh:           make(chan *message),
		bxGrpcCh:       make(chan *message),
		hashes:         make(chan string, bufSize),
		trailNewHashes: utils.NewHashSet(),
		leadNewHashes:  utils.NewHashSet(),
		seenHashes:     make(map[string]*grpcHashEntry),
	}
}

// Run is an entry point to the TxGrpcWSCompareService.
func (s *BlockGrpcWSCompareService) Run(c *cli.Context) error {
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
				if err := file.Sync; err != nil {
					log.Errorf("cannot sync contents of file %q: %v", fileName, err)
				}
				if err := file.Close(); err != nil {
					log.Errorf("cannot close file %q: %v", fileName, err)
				}
			}()

			s.allHashesFile = csv.NewWriter(file)

			if err := s.allHashesFile.Write([]string{
				"TxHash", "BloXroute Time", "Grpc Time", "Time Diff",
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
	s.feedName = c.String(flags.BkFeedName.Name)

	readerGroup.Add(2)

	go s.readFeedFromGRPC(ctx, &readerGroup, s.bxGrpcCh, c.String(flags.GatewayGrpc.Name))

	if c.Bool(flags.UseCloudAPI.Name) {
		go s.readFeedFromBX(
			ctx,
			&readerGroup,
			s.bxCh,
			c.String(flags.CloudAPIWSURI.Name),
			c.String(flags.AuthHeader.Name),
			c.Bool(flags.ExcludeBkContents.Name),
		)
	} else {
		go s.readFeedFromBX(
			ctx,
			&readerGroup,
			s.bxCh,
			c.String(flags.Gateway.Name),
			c.String(flags.AuthHeader.Name),
			c.Bool(flags.ExcludeBkContents.Name),
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
					s.stats(c.Bool(flags.Verbose.Name)),
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

func (s *BlockGrpcWSCompareService) handleUpdates(
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

func (s *BlockGrpcWSCompareService) processFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	blockHash := data.hash
	log.Debugf("got message at %s (BXR node, ALL), blockHash: %s", data.timeReceived, blockHash)

	if data.timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(blockHash)
		return nil
	}

	if entry, ok := s.seenHashes[blockHash]; ok {
		if entry.bxrTimeReceived.IsZero() {
			entry.bxrTimeReceived = data.timeReceived
		}
	} else if data.timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(blockHash) &&
		!s.leadNewHashes.Contains(blockHash) {

		s.seenHashes[blockHash] = &grpcHashEntry{
			hash:            blockHash,
			bxrTimeReceived: data.timeReceived,
		}
	} else {
		s.trailNewHashes.Add(blockHash)
	}

	return nil
}

func (s *BlockGrpcWSCompareService) processFeedFromGRPC(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v", s.feedName, data.err)
	}

	blockHash := data.hash

	log.Debugf("got message at %s (BXR node, ALL), blockHash: %s", data.timeReceived, blockHash)
	if data.timeReceived.Before(s.timeToBeginComparison) {
		s.leadNewHashes.Add(blockHash)
		return nil
	}
	if entry, ok := s.seenHashes[blockHash]; ok {
		if entry.grpcBxrTimeReceived.IsZero() {
			entry.grpcBxrTimeReceived = data.timeReceived
		}
	} else if data.timeReceived.Before(s.timeToEndComparison) &&
		!s.trailNewHashes.Contains(blockHash) &&
		!s.leadNewHashes.Contains(blockHash) {
		s.seenHashes[blockHash] = &grpcHashEntry{
			hash:                blockHash,
			grpcBxrTimeReceived: data.timeReceived,
		}
	} else {
		s.trailNewHashes.Add(blockHash)
	}

	return nil
}

func (s *BlockGrpcWSCompareService) stats(verbose bool) string {
	const timestampFormat = "2006-01-02T15:04:05.000"

	var (
		blockSeenByBothFeedsGatewayFirst          = int64(0)
		blockSeenByBothFeedsGrpcGatewayFirst      = int64(0)
		blockReceivedByGatewayFirstTotalDelta     = int64(0)
		blockReceivedByGrpcGatewayFirstTotalDelta = int64(0)
		newBlockFromGatewayFeedFirst              = 0
		newBlockFromGrpcGatewayFeedFirst          = 0
		totalBlockFromGateway                     = 0
		totalBlockFromGrpcGateway                 = 0
	)

	for blockHash, entry := range s.seenHashes {
		if entry.bxrTimeReceived.IsZero() {
			grpcTimeReceived := entry.grpcBxrTimeReceived

			if s.missingHashesFile != nil {
				line := fmt.Sprintf("%s\n", blockHash)
				if _, err := s.missingHashesFile.WriteString(line); err != nil {
					log.Errorf("cannot add blockHash %q to missing hashes file: %v", blockHash, err)
				}
			}
			if s.allHashesFile != nil {
				record := []string{blockHash, "0", grpcTimeReceived.Format(timestampFormat), "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add blockHash %q to all hashes file: %v", blockHash, err)
				}
			}
			newBlockFromGrpcGatewayFeedFirst++
			totalBlockFromGrpcGateway++
			continue
		}
		if entry.grpcBxrTimeReceived.IsZero() {
			gatewayTimeReceived := entry.bxrTimeReceived

			if s.allHashesFile != nil {
				record := []string{blockHash, gatewayTimeReceived.Format(timestampFormat), "0", "0"}
				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add blockHash %q to all hashes file: %v", blockHash, err)
				}
			}
			newBlockFromGatewayFeedFirst++
			totalBlockFromGateway++
			continue
		}

		var (
			grpcGatewayTimeReceived = entry.grpcBxrTimeReceived
			gatewayTimeReceived     = entry.bxrTimeReceived
			timeReceivedDiff        = gatewayTimeReceived.Sub(grpcGatewayTimeReceived)
		)

		totalBlockFromGateway++
		totalBlockFromGrpcGateway++

		if s.allHashesFile != nil {
			record := []string{
				blockHash,
				gatewayTimeReceived.Format(timestampFormat),
				grpcGatewayTimeReceived.Format(timestampFormat),
				fmt.Sprintf("%d", timeReceivedDiff.Microseconds()),
			}
			if err := s.allHashesFile.Write(record); err != nil {
				log.Errorf("cannot add blockHash %q to all hashes file: %v", blockHash, err)
			}
		}

		switch {
		case gatewayTimeReceived.Before(grpcGatewayTimeReceived):
			newBlockFromGatewayFeedFirst++
			blockSeenByBothFeedsGatewayFirst++
			blockReceivedByGatewayFirstTotalDelta += -timeReceivedDiff.Microseconds()
		case grpcGatewayTimeReceived.Before(gatewayTimeReceived):
			newBlockFromGrpcGatewayFeedFirst++
			blockSeenByBothFeedsGrpcGatewayFirst++
			blockReceivedByGrpcGatewayFirstTotalDelta += timeReceivedDiff.Microseconds()
		}
	}

	var (
		newBlockSeenByBothFeeds                 = blockSeenByBothFeedsGatewayFirst + blockSeenByBothFeedsGrpcGatewayFirst
		txReceivedByGatewayFirstAvgDelta        = int64(0)
		blockReceivedByGrpcGatewayFirstAvgDelta = int64(0)
		blockPercentageSeenByGatewayFirst       = float64(0)
	)

	if blockSeenByBothFeedsGatewayFirst != 0 {
		txReceivedByGatewayFirstAvgDelta = blockReceivedByGatewayFirstTotalDelta / blockSeenByBothFeedsGatewayFirst
	}

	if blockSeenByBothFeedsGrpcGatewayFirst != 0 {
		blockReceivedByGrpcGatewayFirstAvgDelta = blockReceivedByGrpcGatewayFirstTotalDelta / blockSeenByBothFeedsGrpcGatewayFirst
	}

	if newBlockSeenByBothFeeds != 0 {
		blockPercentageSeenByGatewayFirst = float64(blockSeenByBothFeedsGrpcGatewayFirst) / float64(newBlockSeenByBothFeeds)
	}

	var timeAverage = blockPercentageSeenByGatewayFirst*float64(blockReceivedByGrpcGatewayFirstAvgDelta) - (1-blockPercentageSeenByGatewayFirst)*float64(txReceivedByGatewayFirstAvgDelta)
	results := fmt.Sprintf(
		"\nAnalysis of Blocks received on both feeds:\n"+
			"Number of blocks: %d\n"+
			"Number of blocks received from gRPC connection first: %d\n"+
			"Number of blocks received from websocket connection first: %d\n"+
			"Percentage of blocks seen first from gRPC connection: %.2f%%\n"+
			"Average time difference for blocks received first from gRPC connection (us): %d\n"+
			"Average time difference for blocks received first from websocket connection (us): %d\n"+
			"Final calculation (us): %.2f\n"+
			"\nTotal Blocks summary:\n"+
			"Total blocks from gRPC connection: %d\n"+
			"Total blocks from ws connection: %d\n",
		newBlockSeenByBothFeeds,
		blockSeenByBothFeedsGrpcGatewayFirst,
		blockSeenByBothFeedsGatewayFirst,
		blockPercentageSeenByGatewayFirst*100,
		blockReceivedByGrpcGatewayFirstAvgDelta,
		txReceivedByGatewayFirstAvgDelta,
		timeAverage,
		totalBlockFromGrpcGateway,
		totalBlockFromGateway)

	verboseResults := fmt.Sprintf(
		"Number of new blocks received first from gRPC connection: %d\n"+
			"Number of new blocks received first from websocket connection: %d\n"+
			"Total number of blocks seen: %d\n",
		newBlockFromGrpcGatewayFeedFirst,
		newBlockFromGatewayFeedFirst,
		newBlockFromGrpcGatewayFeedFirst+newBlockFromGatewayFeedFirst,
	)

	if verbose {
		results += verboseResults
	}

	return results
}

func (s *BlockGrpcWSCompareService) readFeedFromBX(
	ctx context.Context,
	wg *sync.WaitGroup,
	out chan<- *message,
	uri string,
	authHeader string,
	excludeBlockContents bool,
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

	sub, err := conn.SubscribeBkFeedBX(1, s.feedName, excludeBlockContents)
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
		feedData, err := sub.NextMessage()
		var feedResponse bxBkFeedResponse
		if err := json.Unmarshal(feedData, &feedResponse); err != nil {
			log.Errorf("failed to unmarshal block message: %v", err)
			continue
		}

		blockHash := feedResponse.Params.Result.Hash
		var (
			timeReceived = time.Now()
			msg          = &message{
				hash:         blockHash,
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

func (s *BlockGrpcWSCompareService) clearTrailNewHashes() {
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

func (s *BlockGrpcWSCompareService) drainChannels() {
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

func (s *BlockGrpcWSCompareService) readFeedFromGRPC(ctx context.Context, wg *sync.WaitGroup, out chan<- *message, uri string) {
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
	case "newBlocks":
		stream, err := client.NewBlocks(callContext, &pb.BlocksRequest{})
		if err != nil {
			log.Errorf("could not create newBlocks %v", err)
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
						hash:         data.Hash,
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
	case "bdnBlocks":
		stream, err := client.BdnBlocks(callContext, &pb.BlocksRequest{})
		if err != nil {
			log.Errorf("could not create bdnBlocks %v", err)
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
						hash:         data.Hash,
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
		log.Errorf("feed name %v is not recognized", s.feedName)
		os.Exit(1)
	}
}
