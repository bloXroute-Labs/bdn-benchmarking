package transactions

import (
	"context"
	"encoding/json"
	"fmt"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	EthSubscription = "eth_subscription"
	InvalidNonce    = ""
)

type GethJSONRPC struct {
	uri              string
	authToken        string
	excludeTxContent bool
}

type TransactionInfo struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	Nonce string `json:"nonce"`
}

type EthereumSubscriptionResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Subscription string `json:"subscription"`
		Result       string `json:"result"`
	} `json:"params"`
}

type EthereumJSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id"`
	Result  struct {
		Hash  string `json:"hash"`
		Nonce string `json:"nonce"`
		From  string `json:"from"`
	} `json:"result"`
}

func NewGeth(c *cli.Context, uri string) *GethJSONRPC {
	return &GethJSONRPC{
		uri:              uri,
		authToken:        c.String(flags.BloxrouteAuthHeader.Name),
		excludeTxContent: c.Bool(flags.ExcludeTxContent.Name),
	}
}

func createAndSendFullTxInfo(rpcResponse EthereumJSONRPCResponse, out chan *Message, size int) {
	transaction := &TransactionInfo{
		Hash:  rpcResponse.Result.Hash,
		From:  rpcResponse.Result.From,
		Nonce: rpcResponse.Result.Nonce,
	}
	//convert txInfo to []byte and store it in RawTx
	jsonData, err := json.Marshal(transaction)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	timeReceived := time.Now()

	out <- &Message{
		RawTx:            jsonData,
		FeedReceivedTime: timeReceived,
		Size:             size,
	}
}

func createAndSendOnlyHashTx(subscriptionResponse EthereumSubscriptionResponse, out chan *Message, size int) {
	transaction := &TransactionInfo{
		Hash: subscriptionResponse.Params.Result,
	}
	jsonData, err := json.Marshal(transaction)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	timeReceived := time.Now()

	out <- &Message{
		RawTx:            jsonData,
		FeedReceivedTime: timeReceived,
		Size:             size,
	}
}

func (g GethJSONRPC) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s", g.Name())

	conn, err := ws.NewConnection(g.uri, g.authToken)
	if err != nil {
		log.Errorf("cannot establish connection to %s %s: %v", g.Name(), g.uri, err)
		return
	}
	log.Infof("%s connection to %s established", g.Name(), g.uri)
	defer func() {
		if err := conn.Close(); err != nil {
			log.Errorf("cannot close socket connection to %s: %v", g.uri, err)
		}
	}()
	sub, err := conn.SubscribeTxFeedEth(1)
	if err != nil {
		log.Errorf("cannot subscribe to feed %s : %v", g.Name(), err)
		return
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from feed %s : %v", g.Name(), err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Infof("stop %s feed", g.Name())
			return
		default:
			data, err := sub.NextMessage()
			if err != nil {
				log.Errorf("failed to read new message from feed %s : %v", g.Name(), err)
				continue
			}
			var subscriptionResponse EthereumSubscriptionResponse
			if err := json.Unmarshal(data, &subscriptionResponse); err == nil {
				// if excludeTxContent enable we just want the hash, so we don't need extra request
				if g.excludeTxContent {
					createAndSendOnlyHashTx(subscriptionResponse, out, len(data))
					continue
				}
				// if its eth_subscription we send getTransactionByHash request to node to get extra data, we continue because we will get response in the sub conn
				if subscriptionResponse.Method == EthSubscription {
					if err = conn.GetTransactionByHash(1, subscriptionResponse.Params.Result); err != nil {
						log.Errorf("error in writing to socket %v", err)
						continue
					}
				} else {
					//if its getTransactionByHash response unmarshall and send it to channel
					var rpcResponse EthereumJSONRPCResponse
					if err := json.Unmarshal(data, &rpcResponse); err == nil {
						if rpcResponse.Result.Nonce == InvalidNonce {
							continue
						}
						createAndSendFullTxInfo(rpcResponse, out, len(data))
					} else {
						log.Errorf("Unable to determine the response type data %v", string(data))
						continue
					}
				}
			} else {
				log.Errorf("Unable to unmarshall EthereumSubscriptionResponse data %v", string(data))
				return
			}
		}

	}
}

func (g GethJSONRPC) ParseMessage(message *Message) (*Transaction, error) {
	var transaction TransactionInfo
	//in RawTx we have TransactionInfo so we Unmarshal
	if err := json.Unmarshal(message.RawTx, &transaction); err != nil {
		return nil, err
	}
	t := Transaction{
		Hash: transaction.Hash,
	}
	if !g.excludeTxContent {
		str := strings.TrimPrefix(transaction.Nonce, "0x")
		nonce, err := strconv.ParseUint(str, 16, 64)
		if err != nil {
			log.Errorf("error convert nonce %v", err)
			return nil, err
		}
		t.Nonce = nonce
		t.Sender = transaction.From
	}
	return &t, nil
}

func (g GethJSONRPC) Name() string {
	return "GethJSONRPC"
}
