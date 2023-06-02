package cmpfeeds

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const providerURL = ""
const fileName = "filteredTxs.csv"

var addrFilterMap = map[string]bool{
	"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": true,
	"0x8ad599c3a0ff1de082011efddc58f1908eb6e6d8": true,
}
var topicHashFilterMap = map[string]string{
	"0x0c396cd989a39f4459b5fa1aed6a9a8dcdbc45908acfd67e028cd568da98982c": "BURN",
	"0x7a53080ba414158be7ec69b987b5fb7d07dee101fe85488f0853ae16239d0bde": "MINT",
	"0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67": "SWAP",
}

type txTrace struct {
	Txtrace []struct {
		Ipaddress string `json:"ipAddress"`
		Region    string `json:"region"`
		Txtime    string `json:"txTime"`
		Diff      string `json:"diff"`
		Error     string `json:"error"`
	} `json:"txTrace"`
	Numberofrelays int `json:"numberOfRelays"`
}

// TxFilterService represents a service which filters txs coming from a BX gateway
// based on specified addresses as recipients
type TxFilterService struct {
	handlers              chan handler
	bxCh                  chan *message
	excBkContents         bool
	feedName              string
	filteredTxs           map[string]*txFiletInfo
	allHashesFile         *csv.Writer
	timeToBeginComparison time.Time
	timeToEndComparison   time.Time
	trailNewHashes        utils.HashSet
	mu                    sync.Mutex
}

// NewTxFilterService creates and initializes TxFilterService instance.
func NewTxFilterService() *TxFilterService {
	return &TxFilterService{
		handlers:       make(chan handler),
		bxCh:           make(chan *message),
		filteredTxs:    make(map[string]*txFiletInfo),
		trailNewHashes: utils.NewHashSet(),
	}
}

// Run is an entry point to the BkFeedsCompareService.
func (s *TxFilterService) Run(c *cli.Context) error {
	file, err := os.Create(fileName)
	if err != nil {
		errMsg := fmt.Errorf("cannot open file %q: %v", fileName, err)
		log.Errorf("errMsg: %v", errMsg)
		return errMsg
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
		"Index", "BlockNumber", "TxHash", "TxIndex", "Type", "From", "To", "Gas", "GasPrice", "GasUsed", "CumulativeGasUsed", "Input", "Nonce", "Value", "TxTrace", "IsPrivate",
	}); err != nil {
		return fmt.Errorf("cannot write CSV header of file %q: %v", fileName, err)
	}

	s.excBkContents = c.Bool(flags.ExcludeBkContents.Name)

	var (
		intervalSec  = c.Int(flags.Interval.Name)
		trailTimeSec = c.Int(flags.TrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())

		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	s.timeToEndComparison = s.timeToBeginComparison.Add(time.Second * time.Duration(intervalSec))
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
	go s.handleUpdates(ctx, &handleGroup, c.String(flags.AuthHeader.Name))

	time.Sleep(time.Second * time.Duration(intervalSec))
	time.Sleep(time.Second * time.Duration(trailTimeSec))

	txCount := 1
	s.handlers <- func() error {
		for txHash, entry := range s.filteredTxs {
			if s.allHashesFile != nil {
				txTraceBytes, err := json.Marshal(entry.txTrace)
				if err != nil {
					log.Errorf("failed to marshal txtrace to string %v", entry.txTrace)
					continue
				}

				bigIntGasPrice := new(big.Int)
				_, successGasPriceToBigIng := bigIntGasPrice.SetString(entry.tx.GasPrice, 0)
				if !successGasPriceToBigIng {
					log.Errorf("failed to convert gasPrice: %s from hex to bigIng", entry.tx.GasPrice)
					continue
				}

				bigIntValue := new(big.Int)
				_, successValueToBigIng := bigIntValue.SetString(entry.tx.Value, 0)
				if !successValueToBigIng {
					log.Errorf("failed to convert gasPrice: %s from hex to bigIng", entry.tx.Value)
					continue
				}

				uintNonceValue, err := strconv.ParseUint(entry.tx.Nonce, 0, 64)
				if err != nil {
					log.Errorf("failed to convert nonce: %s from hex to uint64", entry.tx.Nonce)
					continue
				}

				uintGasValue, err := strconv.ParseUint(entry.tx.Gas, 0, 64)
				if err != nil {
					log.Errorf("failed to convert gas: %s from hex to uint64", entry.tx.Gas)
					continue
				}

				record := []string{
					strconv.Itoa(txCount),
					strconv.FormatInt(entry.blockNum, 10),
					txHash,
					entry.additionalFields.Index,
					entry.additionalFields.Type,
					entry.tx.From,
					entry.tx.To,
					strconv.FormatUint(uintGasValue, 10),
					bigIntGasPrice.String(),
					entry.additionalFields.GasUsed,
					entry.additionalFields.CumulativeGasUsed,
					entry.tx.Input,
					strconv.FormatUint(uintNonceValue, 10),
					bigIntValue.String(),
					string(txTraceBytes),
					strconv.FormatBool(entry.isPrivate),
				}

				if err := s.allHashesFile.Write(record); err != nil {
					log.Errorf("cannot add txHash %q to file: %v", txHash, err)
				}
				txCount++
			}
		}
		s.timeToEndComparison = time.Now().Add(time.Second * time.Duration(intervalSec))
		return nil
	}
	cancel()
	readerGroup.Wait()
	handleGroup.Wait()

	return nil
}

func (s *TxFilterService) handleUpdates(
	ctx context.Context,
	wg *sync.WaitGroup,
	authHeader string,
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
				if err := s.processFeedFromBX(data, authHeader); err != nil {
					log.Errorf("error: %v", err)
				}
			default:
				break
			}
		}
	}
}

func (s *TxFilterService) processFeedFromBX(data *message, authHeader string) error {
	if data.err != nil {
		return fmt.Errorf("failed to read message from feed %q: %v",
			s.feedName, data.err)
	}

	if data.timeReceived.Before(s.timeToEndComparison) && !s.trailNewHashes.Contains(data.hash) {
		timeReceived := time.Now()

		var bk bxBkFeedResponseWithTx
		if err := json.Unmarshal(data.bytes, &bk); err != nil {
			return fmt.Errorf("failed to unmarshal message: %v", err)
		}

		bkNum, _ := strconv.ParseInt(bk.Params.Result.Header.Number, 0, 64)

		hash := bk.Params.Result.Hash
		log.Debugf("got message at %s (BXR node, ALL), hash: %s", timeReceived, hash)

		txs := bk.Params.Result.Transactions
		for _, tx := range txs {
			go s.handleTx(tx, bkNum, timeReceived, authHeader)
		}
	} else {
		s.trailNewHashes.Add(data.hash)
	}
	return nil
}

func getTxTraceInfo(txHash string, authHeader string) (*txTrace, error) {
	client := &http.Client{}

	proxyReq, err := http.NewRequest("GET", fmt.Sprintf("https://tx-trace.blxrbdn.com/txtrace/%v?auth_header=%v&detailed=true", txHash, authHeader), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create txtrace request %v", err)
	}

	// Send the request and get response
	resp, err := client.Do(proxyReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send txtrace request: %v", err)
	}

	// Read the response body
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read txtrace response body: %v", err)
	}

	txTraceObj := txTrace{}
	if err = json.Unmarshal(body, &txTraceObj); err != nil {
		return nil, nil
	}

	return &txTraceObj, nil
}

func (s *TxFilterService) updateTxFilterInfo(tx bxBkTx, blockNum int64, timeReceived time.Time, authHeader string, additionalFields txAdditionalFields) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.filteredTxs[tx.Hash]; !ok {
		txTraceInfo, err := getTxTraceInfo(tx.Hash, authHeader)
		var isPrivateTx bool
		if txTraceInfo.Txtrace == nil {
			if err != nil {
				return
			}
			// if transaction was not found in txtrace it is private
			isPrivateTx = true
		}
		s.filteredTxs[tx.Hash] = &txFiletInfo{
			blockNum:         blockNum,
			tx:               tx,
			additionalFields: additionalFields,
			txTrace:          *txTraceInfo,
			isPrivate:        isPrivateTx,
			timestamp:        timeReceived,
		}
		log.Infof("filteredTxs in %v is %v", tx.Hash, s.filteredTxs[tx.Hash])
	}
}

func (s *TxFilterService) handleTx(tx bxBkTx, blockNum int64, timeReceived time.Time, authHeader string) {
	// sleep in order to be sure that the tx is confirmed, and we can get the receipt
	time.Sleep(3 * time.Second)

	client, err := ethclient.Dial(providerURL)
	if err != nil {
		log.Fatal(err)
	}

	receipt, err := client.TransactionReceipt(context.Background(), common.HexToHash(tx.Hash))
	if err != nil {
		log.Warnf("failed to get the transaction receipt for txHash %s: %v blockNum: %d", tx.Hash, err, blockNum)
	} else {
		for _, recLog := range receipt.Logs {
			if addrFilterMap[strings.ToLower(recLog.Address.String())] {
				for _, topic := range recLog.Topics {
					if topicHashFilterMap[topic.String()] != "" {

						txReceipt := txAdditionalFields{
							Index:             strconv.Itoa(int(receipt.TransactionIndex)),
							GasUsed:           strconv.Itoa(int(receipt.GasUsed)),
							CumulativeGasUsed: strconv.Itoa(int(receipt.CumulativeGasUsed)),
							Type:              topicHashFilterMap[topic.String()],
						}

						s.updateTxFilterInfo(tx, blockNum, timeReceived, authHeader, txReceipt)
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

func (s *TxFilterService) clearTrailNewHashes() {
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
