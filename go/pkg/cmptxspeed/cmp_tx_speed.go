package cmptxspeed

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"math/big"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/utils"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"time"
)

// TxSpeedCompareService represents a service which compares transaction sending speed time
// between ETH node and BX gateway.
type TxSpeedCompareService struct{}

// Run is an entry point to the TxSpeedCompareService.
func (s *TxSpeedCompareService) Run(c *cli.Context) error {
	var (
		gasLimit         = int64(22000)
		senderPrivateKey = c.String(flags.SenderPrivateKey.Name)
		bxEndpoint       = c.String(flags.BXEndpoint.Name)
		bxAuthHeader     = c.String(flags.BXAuthHeader.Name)
		numTxGroups      = c.Int(flags.NumTxGroups.Name)
		gasPriceWei      = c.Int64(flags.GasPrice.Name) * params.GWei
		chainID          = c.Int(flags.ChainID.Name)
		delay            = c.Int(flags.Delay.Name)
		nodeEndpoint     = c.String(flags.NodeWSEndpoint.Name)
	)

	secretKey, err := makePrivateKey(senderPrivateKey)
	if err != nil {
		return err
	}

	address, err := getSenderAddress(secretKey)
	if err != nil {
		return err
	}

	nodeConn, err := openConnection(nodeEndpoint, "")
	if err != nil {
		return err
	}
	defer closeConnection(nodeConn, nodeEndpoint)

	bxConn, err := openConnection(bxEndpoint, bxAuthHeader)
	if err != nil {
		return err
	}
	defer closeConnection(bxConn, bxEndpoint)

	nonce, err := getNonce(nodeConn, address)
	if err != nil {
		return err
	}

	balance, err := getBalance(nodeConn, address)
	if err != nil {
		return err
	}

	if expense := int64(numTxGroups) * gasPriceWei * gasLimit; balance < uint64(expense) {
		var (
			requiredEth = float64(expense) / params.Ether
			currentEth  = float64(balance) / params.Ether
		)

		fmt.Printf("Sender %s does not have enough balance for %d groups of transactions.\n"+
			"Sender's balance is %f ETH,\n"+
			"while at least %f ETH is required\n",
			address,
			numTxGroups,
			currentEth,
			requiredEth)

		return nil
	}

	fmt.Printf("Initial check completed. Sleeping %d sec.\n", delay)
	time.Sleep(time.Duration(delay) * time.Second)

	var (
		addr         = common.HexToAddress(address)
		value        = big.NewInt(0)
		limit        = uint64(gasLimit)
		price        = big.NewInt(gasPriceWei)
		signer       = types.NewEIP155Signer(big.NewInt(int64(chainID)))
		groupNumToTx = make(map[int]map[string]string)
	)

	for i := 1; i <= numTxGroups; i++ {
		endpointToTx := make(map[string]string)

		fmt.Printf("Sending tx group %d\n", i)

		// BX transaction
		tx := types.NewTx(&types.LegacyTx{
			To:       &addr,
			Value:    value,
			Gas:      limit,
			GasPrice: price,
			Nonce:    nonce,
			Data:     []byte("0x11111111"),
		})

		bxSignedTx, err := types.SignTx(tx, signer, secretKey)
		if err != nil {
			return err
		}

		bxEncodedTx, err := encodeSignedTx(bxSignedTx)
		if err != nil {
			return err
		}

		endpointToTx[bxEndpoint] = bxSignedTx.Hash().Hex()

		// Node transaction
		tx = types.NewTx(&types.LegacyTx{
			To:       &addr,
			Value:    value,
			Gas:      limit,
			GasPrice: price,
			Nonce:    nonce,
			Data:     []byte("0x22222222"),
		})

		ethSignedTx, err := types.SignTx(tx, signer, secretKey)
		if err != nil {
			return err
		}

		ethEncodedTx, err := encodeSignedTx(ethSignedTx)
		if err != nil {
			return err
		}

		endpointToTx[nodeEndpoint] = ethSignedTx.Hash().Hex()

		bxCh, ethCh := make(chan []byte), make(chan []byte)
		go bxSendTx(bxCh, bxConn, bxEncodedTx[2:])
		go ethSendTx(ethCh, nodeConn, ethEncodedTx)
		bxRes, ethRes := <-bxCh, <-ethCh

		fmt.Printf("blxr response: %s", string(bxRes))
		fmt.Printf("node response: %s", string(ethRes))

		time.Sleep(5 * time.Second)

		nonce++
		groupNumToTx[i] = endpointToTx
		// Add a delay to all the groups except for the last group
		if i < numTxGroups {
			fmt.Printf("Sleeping %d sec.\n", delay)
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	fmt.Println("Sleeping 1 min before checking transaction status.")
	time.Sleep(60 * time.Second)

	var (
		endpointToTxMined = make(map[string]int)
		minedTxNums       = utils.NewHashSet()
		sleepLeftMinute   = 4
	)

	for len(minedTxNums) < numTxGroups && sleepLeftMinute > 0 {
		for groupNum, txMap := range groupNumToTx {
			grpNum := strconv.Itoa(groupNum)

			// Continue for confirmed transactions
			if minedTxNums.Contains(grpNum) {
				continue
			}

			// Check transactions sent to different endpoints and find the confirmed one
			for endpoint, txHash := range txMap {
				confirmed, err := isConfirmed(nodeConn, txHash)
				if err != nil {
					log.Errorf("cannot get tx confirmation, hash: %s, endpoint: %s, error: %v",
						txHash, endpoint, err)
					continue
				}

				if confirmed {
					endpointToTxMined[endpoint]++
					minedTxNums.Add(grpNum)
					break
				}
			}
		}

		// When there is any pending transaction, maximum sleep time is 4 min
		if len(minedTxNums) < numTxGroups {
			fmt.Printf("%d transactions are pending.\n"+
				"Sleeping 1 min before checking status again.\n",
				numTxGroups-len(minedTxNums))

			time.Sleep(60 * time.Second)
			sleepLeftMinute--
		}
	}

	fmt.Printf("\n----------------------------------------------------------------\n"+
		"Sent %d groups of transactions to bloXroute and other ETH endpoints,\n"+
		"%d of them have been confirmed:\n"+
		"Number of confirmed transactions is %d for ETH endpoint %s\n"+
		"Number of confirmed transactions is %d for bloXroute endpoint %s\n",
		numTxGroups,
		len(minedTxNums),
		endpointToTxMined[nodeEndpoint], nodeEndpoint,
		endpointToTxMined[bxEndpoint], bxEndpoint)

	return nil
}

func bxSendTx(out chan<- []byte, conn *ws.Connection, rawTx string) {
	req := ws.NewRequest(1, "blxr_tx", []interface{}{
		map[string]interface{}{
			"transaction": rawTx,
		},
	})

	data, err := conn.Call(req)
	if err != nil {
		out <- []byte(err.Error())
	} else {
		out <- data
	}
}

func ethSendTx(out chan<- []byte, conn *ws.Connection, rawTx string) {
	req := ws.NewRequest(1, "eth_sendRawTransaction", []interface{}{
		rawTx,
	})

	data, err := conn.Call(req)
	if err != nil {
		out <- []byte(err.Error())
	} else {
		out <- data
	}
}

// NewTxSpeedCompareService creates and initializes TxSpeedCompareService instance.
func NewTxSpeedCompareService() *TxSpeedCompareService {
	return &TxSpeedCompareService{}
}

func getSenderAddress(key *ecdsa.PrivateKey) (string, error) {
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}

func getNonce(conn *ws.Connection, address string) (uint64, error) {
	req := ws.NewRequest(1, "eth_getTransactionCount", []interface{}{
		address, "latest",
	})

	data, err := conn.Call(req)
	if err != nil {
		return 0, err
	}

	var res nodeTxCountResponse
	if err = json.Unmarshal(data, &res); err != nil {
		return 0, err
	}

	if res.Error != nil {
		return 0, fmt.Errorf("cannot get nonce: %s", res.Error.Message)
	}

	if res.Result == nil {
		return 0, fmt.Errorf("cannot get nonce: empty response result")
	}

	return parseHexNum(*res.Result)
}

func getBalance(conn *ws.Connection, address string) (uint64, error) {
	req := ws.NewRequest(1, "eth_getBalance", []interface{}{
		address, "latest",
	})

	data, err := conn.Call(req)
	if err != nil {
		return 0, err
	}

	var res nodeBalanceResponse
	if err = json.Unmarshal(data, &res); err != nil {
		return 0, err
	}

	if res.Error != nil {
		return 0, fmt.Errorf("cannot get balance: %s", res.Error.Message)
	}

	if res.Result == nil {
		return 0, fmt.Errorf("cannot get balance: empty response result")
	}

	return parseHexNum(*res.Result)
}

func isConfirmed(conn *ws.Connection, txHash string) (bool, error) {
	req := ws.NewRequest(1, "eth_getTransactionReceipt", []interface{}{
		txHash,
	})

	data, err := conn.Call(req)
	if err != nil {
		return false, err
	}

	var res nodeReceiptResponse
	if err = json.Unmarshal(data, &res); err != nil {
		return false, err
	}

	if res.Error != nil {
		return false, fmt.Errorf(
			"cannot get receipt for transaction %s: %s", txHash, res.Error.Message)
	}

	return res.Result != nil, nil
}

func openConnection(uri, authHeader string) (*ws.Connection, error) {
	log.Debugf("initiating connection to %s", uri)
	conn, err := ws.NewConnection(uri, authHeader)
	if err != nil {
		return nil, err
	}

	log.Debugf("connection to %s established", uri)
	return conn, nil
}

func closeConnection(conn *ws.Connection, uri string) {
	if err := conn.Close(); err != nil {
		log.Errorf("cannot close socket connection to %s: %v", uri, err)
	}
	log.Debugf("connection to %s was closed", uri)
}

func trimHexPrefix(number string) string {
	return strings.Replace(strings.ToLower(number), "0x", "", 1)
}

func parseHexNum(number string) (uint64, error) {
	return strconv.ParseUint(trimHexPrefix(number), 16, 64)
}

func makePrivateKey(key string) (*ecdsa.PrivateKey, error) {
	return crypto.HexToECDSA(key[2:])
}

func encodeSignedTx(signedTx *types.Transaction) (string, error) {
	var buf bytes.Buffer

	if err := signedTx.EncodeRLP(&buf); err != nil {
		return "", err
	}

	return hexutil.Encode(buf.Bytes()), nil
}
