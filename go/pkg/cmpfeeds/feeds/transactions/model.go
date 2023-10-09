package transactions

import (
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
)

type Message struct {
	FeedReceivedTime time.Time
	RawTx            []byte
	Size             int
}

type Transaction struct {
	Nonce  uint64
	Sender string
	Hash   string
}

func newTransaction(ethTx types.Transaction, feed string) (*Transaction, error) {
	sender, err := types.Sender(types.NewLondonSigner(ethTx.ChainId()), &ethTx)
	if err != nil {
		log.Errorf("can not extract sender from transaction %s: %v", feed, err)
		return nil, err
	}

	txHash := ethTx.Hash().String()

	return &Transaction{
		Nonce:  ethTx.Nonce(),
		Sender: strings.ToLower(sender.Hex()),
		Hash:   txHash,
	}, nil
}

func (t Transaction) Key(excludeTxContent bool) string {
	if excludeTxContent {
		return t.Hash
	} else {
		return t.Sender + strconv.FormatUint(t.Nonce, 10)
	}
}

type bxTxFeedResponse struct {
	Params struct {
		Result struct {
			TxHash     string `json:"txHash"`
			RawTx      string `json:"rawTx"`
			TxContents struct {
				GasPrice *string `json:"gasPrice"`
				To       *string `json:"to"`
			} `json:"txContents"`
		} `json:"result"`
	} `json:"params"`
}
