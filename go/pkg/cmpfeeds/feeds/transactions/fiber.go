package transactions

import (
	"context"
	"performance/internal/pkg/flags"
	"sync"
	"time"

	fiber "github.com/chainbound/fiber-go"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type Fiber struct {
	authHeader string
	uri        string
}

const defaultFiberURL = "beta.fiberapi.io:8080"

func NewFiber(c *cli.Context, uri string) *Fiber {
	if uri == "" {
		uri = defaultFiberURL
	}
	return &Fiber{
		authHeader: c.String(flags.FiberAuthKey.Name),
		uri:        uri,
	}
}

func (f Fiber) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s", f.Name())

	client := fiber.NewClient(f.uri, f.authHeader)
	defer client.Close()

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("failed to connect to %s: %v", f.Name(), err)
	}

	ch := make(chan *fiber.Transaction)
	go func() {
		if err := client.SubscribeNewTxs(nil, ch); err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			log.Fatalf("failed to get new transactions from %s :%v", f.Name(), err)
		}
	}()

	log.Infof("connection to %s established", f.Name())

	for {
		select {
		case <-ctx.Done():
			log.Infof("stop %s feed", f.Name())
			return
		case tx := <-ch:
			timeReceived := time.Now()
			txByte, err := tx.ToNative().MarshalBinary()
			if err != nil {
				log.Errorf("could not marshal %s transaction: %v", f.Name(), err)
				continue
			}

			msg := &Message{
				RawTx:            txByte,
				FeedReceivedTime: timeReceived,
				Size:             len(txByte),
			}
			out <- msg
		}
	}

}

func (f Fiber) ParseMessage(message *Message) (*Transaction, error) {
	txStr := hexutil.Encode(message.RawTx)
	txBytes, err := decodeHex(txStr)
	if err != nil {
		return nil, err
	}

	var ethTx types.Transaction
	err = ethTx.UnmarshalBinary(txBytes)
	if err != nil {
		// If UnmarshalBinary failed, we will try RLP in case user made mistake
		e := rlp.DecodeBytes(txBytes, &ethTx)
		if e != nil {
			log.Errorf("could not decode %s transaction: %v", f.Name(), err)
			return nil, err
		}
	}

	return newTransaction(ethTx, f.Name())
}

func (Fiber) Name() string {
	return "FiberTransactions"
}
