package transactions

import (
	"context"
	"encoding/json"
	"fmt"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type GatewayWS struct {
	uri       string
	feedName  string
	authToken string
}

const defaultGatewayWSURI = "ws://127.0.0.1:28333/ws"

func NewGatewayWS(c *cli.Context, uri string) *GatewayWS {
	if uri == "" {
		uri = defaultGatewayWSURI
	}
	return &GatewayWS{
		uri:       uri,
		feedName:  c.String(flags.TxFeedName.Name),
		authToken: c.String(flags.AuthHeader.Name),
	}
}

func (g GatewayWS) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()

	log.Infof("Initiating connection to %s %v", g.Name(), g.uri)

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

	sub, err := conn.SubscribeTxFeedBX(1, g.feedName)
	if err != nil {
		log.Errorf("cannot subscribe to feed %s %s: %v", g.Name(), g.feedName, err)
		return
	}

	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from feed %s %q: %v", g.Name(), g.feedName, err)
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
				log.Errorf("failed to read new message from feed %s %s: %v", g.Name(), g.feedName, err)
				continue
			}
			timeReceived := time.Now()

			out <- &Message{
				RawTx:            data,
				FeedReceivedTime: timeReceived,
				Size:             len(data),
			}
		}

	}
}

func (g GatewayWS) ParseMessage(message *Message) (*Transaction, error) {
	var msg bxTxFeedResponse
	if err := json.Unmarshal(message.RawTx, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ws transaction message: %v", err)
	}

	txBytes, err := decodeHex(msg.Params.Result.RawTx)
	if err != nil {
		return nil, err
	}

	var ethTx types.Transaction
	err = ethTx.UnmarshalBinary(txBytes)
	if err != nil {
		// If UnmarshalBinary failed, we will try RLP in case user made mistake
		e := rlp.DecodeBytes(txBytes, &ethTx)
		if e != nil {
			log.Errorf("could not decode %s transaction: %v", g.Name(), err)
			return nil, err
		}
	}

	return newTransaction(ethTx, g.Name())
}

func (g GatewayWS) Name() string {
	return fmt.Sprintf("GatewayTransactionsWS(%s)", g.uri)
}
