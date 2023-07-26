package cmpfeeds

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	_ "github.com/go-sql-driver/mysql"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const providerURL = ""
const providerURL2 = ""
const mysqlDsn = ""
const csvDir = "csv"
const googleCredentialsFile = "googleClient.json"
const dropboxRefreshToken = ""
const dropboxId = ""
const dropboxSecret = ""
const googleToken = "token.json"

// Test API 2 (Shared with Uniswap)
const googleFolderID = ""

// Test API 1 (Backup)
//const googleFolderID = ""

var addrFilterMap = map[string]bool{
	"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": true,
	"0x8ad599c3a0ff1de082011efddc58f1908eb6e6d8": true,
	"0xa6cc3c2531fdaa6ae1a3ca84c2855806728693e8": true,
	"0x11950d141ecb863f01007add7d1a342041227b58": true,
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
	id                     int
	log                    *logrus.Logger
	handlers               chan handler
	bxCh                   chan *message
	osSigChannel           chan os.Signal
	excBkContents          bool
	feedName               string
	filteredTxs            map[string]map[string]*txFiletInfo // filteredTxs[blockNum][txIdx] -> txFiletInfo
	filteredBlockNums      []int                              // keep the block numbers so that we can log the blocks sorted
	filteredTxIdxsPerBlock map[string][]int                   // keep the tx indexes per block so that we can log tx sorted
	allHashesFile          *csv.Writer
	timeToBeginComparison  time.Time
	timeToEndComparison    time.Time
	mu                     sync.Mutex
}

// NewTxFilterService creates and initializes TxFilterService instance.
func NewTxFilterService() *TxFilterService {
	return &TxFilterService{
		handlers:               make(chan handler),
		bxCh:                   make(chan *message),
		osSigChannel:           make(chan os.Signal, 1),
		filteredTxs:            make(map[string]map[string]*txFiletInfo),
		filteredTxIdxsPerBlock: make(map[string][]int),
	}
}

// Run is an entry point to the BkFeedsCompareService.
func (s *TxFilterService) Run(c *cli.Context, id int, logger *logrus.Logger) error {
	// Write to file always after os exits
	signal.Notify(s.osSigChannel, syscall.SIGINT, syscall.SIGTERM)

	s.id = id
	s.log = logger

	var (
		intervalSec  = flags.Interval.Value
		trailTimeSec = c.Int(flags.TrailTime.Name)
		ctx, cancel  = context.WithCancel(context.Background())
		readerGroup  sync.WaitGroup
		handleGroup  sync.WaitGroup
	)

	s.excBkContents = flags.ExcludeBkContents.Value
	s.timeToBeginComparison = time.Now().UTC()
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

	select {
	// trail time should be more than 10 minutes so that all data will be stored in mySql
	case <-time.After(time.Second*time.Duration(intervalSec) + time.Second*time.Duration(trailTimeSec)):
		s.handlers <- s.writeToFile
	case <-s.osSigChannel:
		err := s.writeToFile()
		if err != nil {
			s.log.Warnf("failed to write records to file: %v", err)
		}
		cancel()
		readerGroup.Wait()
		handleGroup.Wait()
		os.Exit(0)
	}

	cancel()
	readerGroup.Wait()
	handleGroup.Wait()
	return nil
}

func (s *TxFilterService) writeToFile() error {
	fileName := fmt.Sprintf("uniswapAnalysis-%s.csv", s.timeToBeginComparison.Format("020120061504"))

	// check if file exists
	_, fileExistsErr := os.Stat(filepath.Join(csvDir, fileName))

	file, err := os.OpenFile(filepath.Join(csvDir, fileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		errMsg := fmt.Errorf("cannot open file %q: %v", fileName, err)
		return errMsg
	}

	defer func() {
		if s.allHashesFile != nil {
			s.allHashesFile.Flush()
		}
		if err = file.Sync(); err != nil {
			s.log.Errorf("cannot sync contents of file %q: %v", fileName, err)
		}
		if err = file.Close(); err != nil {
			s.log.Errorf("cannot close file %q: %v", fileName, err)
		}
		if err = s.uploadFileToDrive(fileName); err != nil {
			s.log.Errorf("failed to upload file to goole drive %q: %v", fileName, err)
		}
		if err = s.uploadFileToDropbox(fileName); err != nil {
			s.log.Errorf("failed to upload file to dropbox %q: %v", fileName, err)
		}
	}()

	s.allHashesFile = csv.NewWriter(file)

	if os.IsNotExist(fileExistsErr) {
		if err := s.allHashesFile.Write([]string{
			"Index", "BlockNumber", "TxHash", "TxIndex", "Type", "From", "To", "Gas", "GasPrice", "GasUsed", "CumulativeGasUsed", "Input", "Nonce", "Value", "TxTrace", "IsPrivate", "privateTxTime",
		}); err != nil {
			return fmt.Errorf("cannot write CSV header of file %q: %v", fileName, err)
		}
	}

	db, err := sql.Open("mysql", mysqlDsn)
	if err != nil {
		s.log.Warnf("error connecting to MySQL: %v", err)
	}
	defer func(db *sql.DB) {
		if db != nil {
			err = db.Close()
			if err != nil {
				s.log.Warnf("error closing connection to MySQL: %v", err)
			}
		}
	}(db)

	txCount := 1
	// sort processed block nums and get each entry from map
	sort.Ints(s.filteredBlockNums)
	for _, blockNum := range s.filteredBlockNums {
		blockNumStr := strconv.FormatInt(int64(blockNum), 10)
		txsEntry, ok := s.filteredTxs[blockNumStr]
		if !ok {
			s.log.Errorf("failed failed to read txs from map, block number: %v", blockNum)
			continue
		}

		// sort tx indexes for each block and get each entry from the nested map
		sort.Ints(s.filteredTxIdxsPerBlock[blockNumStr])
		for _, txIdx := range s.filteredTxIdxsPerBlock[blockNumStr] {
			txIdxStr := strconv.FormatInt(int64(txIdx), 10)
			txEntry, ok := txsEntry[txIdxStr]
			if !ok {
				s.log.Errorf("failed failed to read tx from map, block number: %v,  tx index: %v", blockNum, txIdx)
				continue
			}

			if s.allHashesFile != nil {
				txTraceBytes, err := json.Marshal(txEntry.txTrace)
				if err != nil {
					s.log.Errorf("failed to marshal txtrace to string %v", txEntry.txTrace)
					continue
				}

				bigIntGasPrice := new(big.Int)
				_, successGasPriceToBigIng := bigIntGasPrice.SetString(txEntry.tx.GasPrice, 0)
				if !successGasPriceToBigIng {
					s.log.Errorf("failed to convert gasPrice: %s from hex to bigIng", txEntry.tx.GasPrice)
					continue
				}

				bigIntValue := new(big.Int)
				_, successValueToBigIng := bigIntValue.SetString(txEntry.tx.Value, 0)
				if !successValueToBigIng {
					s.log.Errorf("failed to convert gasPrice: %s from hex to bigIng", txEntry.tx.Value)
					continue
				}

				uintNonceValue, err := strconv.ParseUint(txEntry.tx.Nonce, 0, 64)
				if err != nil {
					s.log.Errorf("failed to convert nonce: %s from hex to uint64", txEntry.tx.Nonce)
					continue
				}

				uintGasValue, err := strconv.ParseUint(txEntry.tx.Gas, 0, 64)
				if err != nil {
					s.log.Errorf("failed to convert gas: %s from hex to uint64", txEntry.tx.Gas)
					continue
				}

				var privateTxTime string
				if db != nil && txEntry.isPrivate {
					privateTxTime, err = getBxTimestamp(db, txEntry.tx.Hash)
					if err != nil {
						s.log.Warnf("error while fetching bxTimestamp for tx: %v, error: %v", txEntry.tx.Hash, err)
					}
				}

				record := []string{
					strconv.Itoa(txCount),
					blockNumStr,
					txEntry.tx.Hash,
					txEntry.additionalFields.Index,
					txEntry.additionalFields.Type,
					txEntry.tx.From,
					txEntry.tx.To,
					strconv.FormatUint(uintGasValue, 10),
					bigIntGasPrice.String(),
					txEntry.additionalFields.GasUsed,
					txEntry.additionalFields.CumulativeGasUsed,
					txEntry.tx.Input,
					strconv.FormatUint(uintNonceValue, 10),
					bigIntValue.String(),
					string(txTraceBytes),
					strconv.FormatBool(txEntry.isPrivate),
					privateTxTime,
				}

				if err := s.allHashesFile.Write(record); err != nil {
					s.log.Errorf("cannot add txHash %q to file: %v", txEntry.tx.Hash, err)
				}
				txCount++
			}
		}
	}
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
				s.log.Errorf("error in update function: %v", err)
			}
		default:
			select {
			case data, ok := <-s.bxCh:
				if !ok {
					continue
				}
				if err := s.processFeedFromBX(data, authHeader); err != nil {
					s.log.Errorf("error: %v", err)
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

	if data.timeReceived.Before(s.timeToEndComparison) {
		var bk bxBkFeedResponseWithTx
		if err := json.Unmarshal(data.bytes, &bk); err != nil {
			return fmt.Errorf("failed to unmarshal message: %v", err)
		}

		bkNum, _ := strconv.ParseInt(bk.Params.Result.Header.Number, 0, 64)

		hash := bk.Params.Result.Hash
		s.log.Debugf("got message at %s (BXR node, ALL), hash: %s", data.timeReceived, hash)

		txs := bk.Params.Result.Transactions
		for _, tx := range txs {
			go s.handleTx(tx, bkNum, data.timeReceived, authHeader)
		}
	}
	return nil
}

func getBxTimestamp(db *sql.DB, txHash string) (string, error) {
	query := "SELECT CAST(timestamp AS DATETIME) AS timestamp FROM private_transactions where tx_hash = ? UNION SELECT CAST(timestamp AS DATETIME) AS timestamp FROM mev_bundle_transactions WHERE tx_hash = ?"
	var timestamp string
	trimmedHash := strings.TrimPrefix(txHash, "0x")
	err := db.QueryRow(query, trimmedHash, trimmedHash).Scan(&timestamp)
	if err != nil {
		return "", fmt.Errorf("error executing query: %v", err)
	}
	return timestamp, nil
}

func getTxReceipt(txHash string) (*types.Receipt, error) {
	// sleep in order to be sure that the tx is confirmed, and we can get the receipt
	time.Sleep(5 * time.Second)

	client, err := ethclient.Dial(providerURL)
	if err != nil {
		return nil, err
	}

	// connect to second client for redundancy
	client2, err := ethclient.Dial(providerURL2)
	if err != nil {
		return nil, err
	}

	maxRetries := 60
	var receipt *types.Receipt
	var retryCount int
	for retryCount < maxRetries {
		receipt, err = client.TransactionReceipt(context.Background(), common.HexToHash(txHash))
		if err == nil {
			return receipt, nil
		}

		receipt, err = client2.TransactionReceipt(context.Background(), common.HexToHash(txHash))
		if err == nil {
			return receipt, nil
		}

		time.Sleep(5 * time.Second)
		retryCount++
	}

	return nil, err
}

func getTxTraceInfo(txHash string, authHeader string) (*txTrace, error) {
	maxRetries := 5
	var txTraceResp *txTrace
	var retryCount int
	var err error
	for retryCount < maxRetries {
		txTraceResp, err = txTraceRequest(txHash, authHeader)
		if err == nil {
			return txTraceResp, nil
		}

		time.Sleep(5 * time.Second)
		retryCount++
	}

	return nil, err
}

func txTraceRequest(txHash string, authHeader string) (*txTrace, error) {
	client := &http.Client{}
	txTraceObj := &txTrace{}

	proxyReq, err := http.NewRequest("GET", fmt.Sprintf("https://tx-trace.blxrbdn.com/txtrace/%v?auth_header=%v&detailed=true", txHash, authHeader), nil)
	if err != nil {
		return txTraceObj, fmt.Errorf("failed to create txtrace request %v", err)
	}

	resp, err := client.Do(proxyReq)
	if err != nil {
		return txTraceObj, fmt.Errorf("failed to send txtrace request: %v", err)
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return txTraceObj, fmt.Errorf("failed to read txtrace response body: %v", err)
	}

	if strings.Contains(string(body), "reached its daily limit") {
		return txTraceObj, fmt.Errorf("%v", string(body))
	}

	if err = json.Unmarshal(body, &txTraceObj); err != nil {
		return txTraceObj, err
	}

	return txTraceObj, nil
}

func (s *TxFilterService) updateTxFilterInfo(tx bxBkTx, blockNum int64, timeReceived time.Time, authHeader string, additionalFields txAdditionalFields) {
	s.mu.Lock()
	defer s.mu.Unlock()

	blockNumStr := strconv.FormatInt(blockNum, 10)

	filteredTxsEntry, blockFound := s.filteredTxs[blockNumStr]
	if blockFound {
		if _, txIdxFound := filteredTxsEntry[additionalFields.Index]; txIdxFound {
			// duplicate entry
			return
		}
	} else {
		s.filteredBlockNums = append(s.filteredBlockNums, int(blockNum))
		s.filteredTxs[blockNumStr] = make(map[string]*txFiletInfo)
	}

	txIdx, err := strconv.ParseInt(additionalFields.Index, 10, 0)
	if err != nil {
		return
	}

	var isPrivateTx bool
	txTraceInfo, err := getTxTraceInfo(tx.Hash, authHeader)
	if txTraceInfo.Txtrace == nil {
		if err != nil {
			s.log.Warnf("failed to fetch txTrace for tx: %v, error: %v", tx.Hash, err)
			return
		}
		// if transaction was not found in txtrace it is private
		isPrivateTx = true
	}

	txFilet := &txFiletInfo{
		tx:               tx,
		additionalFields: additionalFields,
		txTrace:          *txTraceInfo,
		isPrivate:        isPrivateTx,
		timestamp:        timeReceived,
	}

	s.filteredTxs[blockNumStr][additionalFields.Index] = txFilet
	s.filteredTxIdxsPerBlock[blockNumStr] = append(s.filteredTxIdxsPerBlock[blockNumStr], int(txIdx))

	s.log.Infof("Processed tx: BlockNum: %v, TxIdx %v, TxHash: %v, TxType: %v", blockNum, txIdx, tx.Hash, additionalFields.Type)
}

func (s *TxFilterService) handleTx(tx bxBkTx, blockNum int64, timeReceived time.Time, authHeader string) {
	receipt, err := getTxReceipt(tx.Hash)
	if err != nil {
		s.log.Warnf("failed to get the transaction receipt for txHash %s: %v blockNum: %d", tx.Hash, err, blockNum)
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

	for {
		s.log.Infof("Initiating connection to: %s", uri)
		conn, err := ws.NewConnection(uri, authHeader)
		if err != nil {
			s.log.Errorf("cannot establish connection to %s: %v", uri, err)
			// Retry after a certain period
			time.Sleep(time.Second * 5)
			continue
		}
		s.log.Infof("Connection to %s established", uri)

		sub, err := conn.SubscribeBkFeedBX(1, s.feedName, s.excBkContents)
		if err != nil {
			s.log.Errorf("cannot subscribe to feed %q: %v", s.feedName, err)
			conn.Close()
			time.Sleep(time.Second * 5)
			continue
		}

		defer func() {
			if err := sub.Unsubscribe(); err != nil {
				s.log.Errorf("cannot unsubscribe from feed %q: %v", s.feedName, err)
			}
			if err := conn.Close(); err != nil {
				s.log.Errorf("cannot close socket connection to %s: %v", uri, err)
			}
		}()

		for {
			var (
				data, err = sub.NextMessage()
				msg       = &message{
					bytes:        data,
					err:          err,
					timeReceived: time.Now().UTC(),
				}
			)

			select {
			case <-ctx.Done():
				return
			case out <- msg:
			}

			if err != nil {
				s.log.Errorf("socket connection error: %v", err)
				break
			}
		}
	}
}

func (s *TxFilterService) uploadFileToDrive(fileName string) error {
	// Configure connection
	credentials, err := ioutil.ReadFile(googleCredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %v", err)
	}
	config, err := google.ConfigFromJSON(credentials, drive.DriveScope)
	if err != nil {
		return fmt.Errorf("failed to parse credentials file: %v", err)
	}

	f, err := os.Open(googleToken)
	if err != nil {
		return fmt.Errorf("failed to parse token file: %v", err)
	}
	defer f.Close()
	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)

	client := config.Client(context.Background(), token)
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to retrieve Drive client: %v", err)
	}

	// Upload file
	file, err := os.Open(filepath.Join(csvDir, fileName))
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	fileMetadata := &drive.File{
		Name:     fileName,
		Parents:  []string{googleFolderID},
		MimeType: "application/vnd.google-apps.spreadsheet",
	}

	_, err = service.Files.Create(fileMetadata).Media(file, googleapi.ContentType("text/csv")).SupportsAllDrives(true).Do()
	if err != nil {
		return fmt.Errorf("failed to upload file to google drive: %v", err)
	}
	return nil
}

func (s *TxFilterService) uploadFileToDropbox(fileName string) error {
	file, err := os.Open(filepath.Join(csvDir, fileName))
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	client := &http.Client{}
	data := url.Values{}
	data.Set("refresh_token", dropboxRefreshToken)
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", dropboxId)
	data.Set("client_secret", dropboxSecret)

	req, err := http.NewRequest("POST", "https://api.dropbox.com/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("error creating request to dropbox %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error making request to dropbox api: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error refreshing access token. Status: %v", resp.Status)
	}

	var response struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return fmt.Errorf("error parsing response body: %v", err)
	}

	accessToken := response.AccessToken

	req, err = http.NewRequest("POST", "https://content.dropboxapi.com/2/files/upload", file)
	if err != nil {
		return fmt.Errorf("error creating request to dropbox %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Dropbox-API-Arg", "{\"autorename\":false,\"mode\":\"add\",\"mute\":false,\"path\":\"/bloXroute Uniswap/"+fileName+"\",\"strict_conflict\":false}")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("error making request to dropbox api: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error while reading response body from dropbox api: %v\n", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error uploading file to dropbox. Status: %v Body: %v", resp.Status, string(body))
	}
	return nil
}
