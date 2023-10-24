package transactions

import (
	"context"
	"encoding/json"
	"errors"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"sync"
	"time"

	"performance/pkg/constant"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	EthSubscription = "eth_subscription"
	InvalidNonce    = ""
)

type GethJSONRPC struct {
	uri      string
	strategy strategy
}

type strategy interface {
	receive(out chan *Message, data []byte, timeReceived time.Time, conn *ws.Connection)
	parse(rawMsg []byte) (*Transaction, error)
}

type onlyHash struct {
}

func newOnlyHash() strategy {
	return &onlyHash{}
}

func (oh *onlyHash) receive(out chan *Message, data []byte, timeReceived time.Time, conn *ws.Connection) {
	out <- &Message{
		RawTx:            data,
		FeedReceivedTime: timeReceived,
		Size:             len(data),
	}
}
func (oh *onlyHash) parse(rawMsg []byte) (*Transaction, error) {
	var ethSubResponse EthereumSubscriptionResponse
	if err := json.Unmarshal(rawMsg, &ethSubResponse); err != nil {
		return nil, err
	}
	return &Transaction{
		Hash: ethSubResponse.Params.Result,
	}, nil
}

type withContent struct {
}

func newWithContent() strategy {
	return &withContent{}
}

func (tc *withContent) receive(out chan *Message, data []byte, timeReceived time.Time, conn *ws.Connection) {
	var subscriptionResponse EthereumSubscriptionResponse
	err := json.Unmarshal(data, &subscriptionResponse)
	if err != nil {
		log.Errorf("Unable to unmarshall EthereumSubscriptionResponse data %v", string(data))
		return
	}
	// if its eth_subscription we send getTransactionByHash request to node
	if subscriptionResponse.Method == EthSubscription {
		if err := conn.RequestTransactionByHash(1, subscriptionResponse.Params.Result); err != nil {
			log.Errorf("error sending RequestTransactionByHash req, %v", err)
			return
		}
	} else {
		out <- &Message{
			RawTx:            data,
			FeedReceivedTime: timeReceived,
			Size:             len(data),
		}
	}
}
func (tc *withContent) parse(rawMsg []byte) (*Transaction, error) {
	var rpcResponse JSONResponse
	err := json.Unmarshal(rawMsg, &rpcResponse)
	if err != nil {
		return nil, err
	}
	if rpcResponse.Result == nil {
		return nil, constant.EmptyResponseFromGeth
	}
	if rpcResponse.Result.Nonce == InvalidNonce {
		return nil, errors.New("got invalid nonce from geth")
	}
	str := strings.TrimPrefix(rpcResponse.Result.Nonce, "0x")
	nonce, err := strconv.ParseUint(str, 16, 64)
	if err != nil {
		log.Errorf("error convert nonce %v", err)
		return nil, err
	}
	return &Transaction{
		Nonce:  nonce,
		Sender: rpcResponse.Result.From,
		Hash:   rpcResponse.Result.Hash,
	}, nil
}

type EthereumSubscriptionResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Subscription string `json:"subscription"`
		Result       string `json:"result"`
	} `json:"params"`
}
type JSONResponse struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      int     `json:"id"`
	Result  *Result `json:"result"`
}

type Result struct {
	Hash  string `json:"hash"`
	Nonce string `json:"nonce"`
	From  string `json:"from"`
}

func NewGeth(c *cli.Context, uri string) *GethJSONRPC {
	g := GethJSONRPC{
		uri: uri,
	}

	if c.IsSet(flags.ExcludeTxContent.Name) {
		g.strategy = newOnlyHash()
	} else {
		g.strategy = newWithContent()
	}
	return &g
}

func (g GethJSONRPC) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s", g.Name())

	conn, err := ws.NewConnection(g.uri, "")
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
			timeReceived := time.Now()
			if err != nil {
				log.Errorf("failed to read new message from feed %s : %v", g.Name(), err)
				continue
			}
			g.strategy.receive(out, data, timeReceived, conn)
		}
	}
}

func (g GethJSONRPC) ParseMessage(message *Message) (*Transaction, error) {
	return g.strategy.parse(message.RawTx)
}

func (g GethJSONRPC) Name() string {
	return "GethJSONRPC"
}
