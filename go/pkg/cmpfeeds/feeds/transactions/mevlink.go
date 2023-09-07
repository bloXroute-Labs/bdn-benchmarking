package transactions

import (
	"context"
	"performance/internal/pkg/flags"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	mlstreamer "github.com/mevlink/streamer-go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type Mevlink struct {
	apiKey     string
	apiSecret  string
	networkNum int
}

func NewMevlink(c *cli.Context) *Mevlink {
	return &Mevlink{
		apiKey:     c.String(flags.MEVLinkAPIKey.Name),
		apiSecret:  c.String(flags.MEVLinkAPISecret.Name),
		networkNum: c.Int(flags.NetworkNumber.Name),
	}
}

func (m Mevlink) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s", m.Name())

	str := mlstreamer.NewStreamer(m.apiKey, m.apiSecret, mlstreamer.Network(m.networkNum))
	str.OnTransaction(func(txb []byte, hash mlstreamer.NullableHash, noticed, propagated time.Time) {
		timeReceived := time.Now()
		msg := &Message{
			RawTx:            txb,
			FeedReceivedTime: timeReceived,
			Size:             len(txb),
		}
		out <- msg
	})

	go func() {
		<-ctx.Done()
		log.Infof("Stop %s feed", m.Name())
		str.Stop()
	}()

	if err := str.Stream(); err != nil {
		log.Error(err)
	}
}

func (m Mevlink) ParseMessage(message *Message) (*Transaction, error) {
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
			log.Errorf("could not decode %s transaction: %v", m.Name(), err)
			return nil, err
		}
	}

	return newTransaction(ethTx, m.Name())
}

func (Mevlink) Name() string {
	return "Mevlink"
}
