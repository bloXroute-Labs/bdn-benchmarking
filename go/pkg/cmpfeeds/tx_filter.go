package cmpfeeds

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"strconv"
	"sync"
	"time"
)

const blockCountToCheck = 100
const providerURL = "https://mainnet.infura.io/v3/d5575d78e263494393f16cc00654a19b"
const filename = "filteredTxs"

var toAddrFilterMap = map[string]bool{
	"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": true,
	"0x8ad599c3a0ff1de082011efddc58f1908eb6e6d8": true,
}
var topicHashFilterMap = map[string]bool{
	"0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67": true,
	"0x7a53080ba414158be7ec69b987b5fb7d07dee101fe85488f0853ae16239d0bde": true,
	"0x0c396cd989a39f4459b5fa1aed6a9a8dcdbc45908acfd67e028cd568da98982c": true,
}

// TxFilterService represents a service which filters txs coming from a BX gateway
// based on specified addresses as recipients
type TxFilterService struct {
	bxCh          chan *message
	excBkContents bool
	feedName      string
	filteredTxs   []string
}

// NewTxFilterService creates and initializes TxFilterService instance.
func NewTxFilterService() *TxFilterService {
	return &TxFilterService{
		bxCh: make(chan *message),
	}
}

// Run is an entry point to the BkFeedsCompareService.
func (s *TxFilterService) Run(c *cli.Context) error {
	stop := make(chan struct{})

	s.excBkContents = c.Bool(flags.ExcludeBkContents.Name)

	var (
		ctx, cancel = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	s.feedName = c.String(flags.BkFeedName.Name)

	var bxURI string
	if c.Bool(flags.UseCloudAPI.Name) {
		bxURI = c.String(flags.CloudAPIWSURI.Name)
	} else {
		bxURI = c.String(flags.Gateway.Name)
	}

	readerGroup.Add(1)
	go s.readFeedFromBX(
		ctx,
		&readerGroup,
		s.bxCh,
		bxURI,
		c.String(flags.AuthHeader.Name),
	)

	handleGroup.Add(1)
	go s.handleUpdates(ctx, stop, &handleGroup)

	<-stop
	cancel()
	readerGroup.Wait()
	handleGroup.Wait()

	file, err := os.Create(filename)
	if err != nil {
		log.Fatalf("failed to create file: %v", err)
	}
	defer func(file *os.File) {
		err = file.Close()
		if err != nil {
			log.Errorf("failed to close file: %v", err)
		}
	}(file)

	for _, tx := range s.filteredTxs {
		_, err = file.WriteString(tx + "\n\n")
		if err != nil {
			log.Fatalf("failed to write file: %v", err)
		}
	}

	return nil
}

func (s *TxFilterService) handleUpdates(
	ctx context.Context,
	stop chan struct{},
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	blockCount := 1

	for {
		select {
		case <-ctx.Done():
			return
		default:
			select {
			case data, ok := <-s.bxCh:
				if !ok {
					continue
				}
				if blockCount > blockCountToCheck {
					stop <- struct{}{}
				} else {
					log.Infof("Block No: %d", blockCount)
					if err := s.processFeedFromBX(data); err != nil {
						log.Errorf("error: %v", err)
					}
				}
				blockCount++
			default:
				break
			}
		}
	}
}

func (s *TxFilterService) processFeedFromBX(data *message) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	timeReceived := time.Now()

	var bk bxBkFeedResponseWithTx
	if err := json.Unmarshal(data.bytes, &bk); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	bkNum, _ := strconv.ParseInt(bk.Params.Result.Header.Number, 0, 64)

	log.Infof("Block number: %d", bkNum)
	hash := bk.Params.Result.Hash
	log.Debugf("got message at %s (BXR node, ALL), hash: %s", timeReceived, hash)

	txs := bk.Params.Result.Transactions
	for _, tx := range txs {
		go s.handleTx(tx, bkNum)
	}
	return nil
}

func (s *TxFilterService) handleTx(tx bxBkTx, blockNum int64) {
	// sleep in order to be sure that the tx is confirmed
	// and we can get the receipt
	time.Sleep(2 * time.Second)

	txBytes, err := json.Marshal(tx)
	if toAddrFilterMap[tx.To] {
		if err != nil {
			log.Warnf("failed to unmarshal message: %v", err)
		}
		s.filteredTxs = append(s.filteredTxs, string(txBytes))
	} else {
		client, err := ethclient.Dial(providerURL)
		if err != nil {
			log.Fatal(err)
		}

		receipt, err := client.TransactionReceipt(context.Background(), common.HexToHash(tx.Hash))
		if err != nil {
			log.Warnf("failed to get the transaction receipt for txHash %s: %v blockNUm: %d", tx.Hash, err, blockNum)
		} else {
			for _, recLog := range receipt.Logs {
				for _, topic := range recLog.Topics {
					if topicHashFilterMap[topic.String()] {
						s.filteredTxs = append(s.filteredTxs, string(txBytes))
					}
				}
			}
		}
	}
}

func (s *TxFilterService) readFeedFromBX(
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

	sub, err := conn.SubscribeBkFeedBX(1, s.feedName, s.excBkContents)

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
