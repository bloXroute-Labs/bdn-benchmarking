package cmpfeeds

import (
	"context"
	"fmt"
	"performance/internal/pkg/flags"
	"performance/pkg/cmpfeeds/feeds/transactions"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type transactionSender interface {
	Receive(ctx context.Context, wg *sync.WaitGroup, out chan *transactions.Message)
	SendBatch(ctx context.Context, wg *sync.WaitGroup, out chan *transactions.BatchMessage)
	ParseMessage(message *transactions.Message) (*transactions.Transaction, error)
	Name() string
}

type tx struct {
	idx               int
	senderName        string
	sentTimestamp     time.Time
	feedTimeReceived  time.Time
	responseTimestamp time.Time
}

type CompareSendingTransactionsService struct {
	handlers chan handler

	firstSender  transactionSender
	secondSender transactionSender

	transactionsFeedChan chan *transactions.Message
	firstSenderChan      chan *transactions.BatchMessage
	secondSenderChan     chan *transactions.BatchMessage

	txsSent  map[string]tx
	txsMutex sync.Mutex
}

func NewCompareSendingTransactionsService() *CompareSendingTransactionsService {
	const bufSize = 10000

	return &CompareSendingTransactionsService{
		handlers:             make(chan handler),
		transactionsFeedChan: make(chan *transactions.Message, bufSize),
		firstSenderChan:      make(chan *transactions.BatchMessage, bufSize),
		secondSenderChan:     make(chan *transactions.BatchMessage, bufSize),
		txsSent:              make(map[string]tx),
	}
}

func (cs *CompareSendingTransactionsService) senderBuilders(c *cli.Context, feedName, uri string) (transactionSender, error) {
	switch feedName {
	case "GatewayWS":
		return transactions.NewGatewayWS(c, uri), nil
	case "GatewayGRPC":
		return transactions.NewGatewayGRPC(c, uri), nil
	}

	return nil, fmt.Errorf("sender: %s is not supported", feedName)
}

func (cs *CompareSendingTransactionsService) Run(c *cli.Context) error {
	var err error
	cs.firstSender, err = cs.senderBuilders(c, c.String(flags.FirstFeed.Name), c.String(flags.FirstFeedURI.Name))
	if err != nil {
		return err
	}

	cs.secondSender, err = cs.senderBuilders(c, c.String(flags.SecondFeed.Name), c.String(flags.SecondFeedURI.Name))
	if err != nil {
		return err
	}

	var (
		ctx, cancel = context.WithCancel(context.Background())
		senderGroup sync.WaitGroup
		readerGroup sync.WaitGroup
		handleGroup sync.WaitGroup
	)

	readerGroup.Add(1)
	go cs.secondSender.Receive(
		ctx,
		&readerGroup,
		cs.transactionsFeedChan,
	)
	time.Sleep(5 * time.Second)

	senderGroup.Add(2)
	go cs.firstSender.SendBatch(
		ctx,
		&senderGroup,
		cs.firstSenderChan,
	)
	go cs.secondSender.SendBatch(
		ctx,
		&senderGroup,
		cs.secondSenderChan,
	)

	handleGroup.Add(1)
	go cs.handleUpdates(ctx, &handleGroup)

	time.Sleep(5 * time.Second)
	cancel()
	readerGroup.Wait()
	senderGroup.Wait()
	handleGroup.Wait()

	for _, data := range cs.txsSent {
		//fmt.Printf("txHash: %s\nsenderName: %v, txIndex: %v, sentTimestamp: %v, responseTimestamp: %v, feedTimeReceived: %v\n", txHash, data.senderName, data.idx, data.sentTimestamp, data.responseTimestamp, data.feedTimeReceived)
		if data.idx == 9 {
			fmt.Printf("senderName: %v, sentTimestamp: %v\n", data.senderName, data.sentTimestamp)
			fmt.Printf("%v: Batch reached feed in %v\n\n", data.senderName, data.feedTimeReceived.Sub(data.sentTimestamp))
		}
	}

	return nil
}

func (cs *CompareSendingTransactionsService) handleUpdates(
	ctx context.Context,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-cs.transactionsFeedChan:
			if !ok {
				continue
			}

			parsedTx, err := cs.secondSender.ParseMessage(data)
			if err != nil {
				log.Errorf("error parsing tx from feed: %v", err)
				continue
			}

			if elem, exists := cs.txsSent[parsedTx.Hash]; exists {
				updatedElem := elem
				updatedElem.feedTimeReceived = data.FeedReceivedTime
				cs.txsMutex.Lock()
				cs.txsSent[parsedTx.Hash] = updatedElem
				cs.txsMutex.Unlock()
			} else {
				continue
			}

		case data, ok := <-cs.firstSenderChan:
			if !ok {
				continue
			}

			for _, txHashIdx := range data.TxHashes {
				txSent := tx{
					senderName:        cs.firstSender.Name(),
					idx:               int(txHashIdx.Idx),
					sentTimestamp:     data.RequestTime,
					responseTimestamp: data.ResponseTime,
				}

				cs.txsMutex.Lock()
				cs.txsSent[fmt.Sprintf("0x%v", txHashIdx.TxHash)] = txSent
				cs.txsMutex.Unlock()
			}

		case data, ok := <-cs.secondSenderChan:
			if !ok {
				continue
			}

			for _, txHashIdx := range data.TxHashes {
				txSent := tx{
					senderName:        cs.secondSender.Name(),
					idx:               int(txHashIdx.Idx),
					sentTimestamp:     data.RequestTime,
					responseTimestamp: data.ResponseTime,
				}

				cs.txsMutex.Lock()
				cs.txsSent[fmt.Sprintf("0x%v", txHashIdx.TxHash)] = txSent
				cs.txsMutex.Unlock()
			}
		}
	}
}
