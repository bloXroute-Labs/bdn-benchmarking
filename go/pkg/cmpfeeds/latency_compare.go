package cmpfeeds

import (
	"context"
	"fmt"
	"os"
	"performance/internal/pkg/flags"
	"performance/pkg/constant"
	"time"

	pb "github.com/bloXroute-Labs/gateway/v2/protobuf"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type txEntry struct {
	feedTime time.Time
	txTime   time.Time
}

type CompareFeedTransactionsService struct {
	hashWithTimes       map[string]txEntry
	txsReceivedFromFeed map[time.Time][]*pb.Tx
}

func NewCompareFeedTransactions() *CompareFeedTransactionsService {
	return &CompareFeedTransactionsService{
		hashWithTimes:       make(map[string]txEntry),
		txsReceivedFromFeed: make(map[time.Time][]*pb.Tx, constant.MapSize),
	}
}

func (cf *CompareFeedTransactionsService) Run(c *cli.Context) error {
	log.Info("Initiating connection")
	dialOptions := []grpc.DialOption{
		grpc.WithInitialWindowSize(constant.WindowSize),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	conn, err := grpc.Dial(c.String(flags.FirstFeedURI.Name), dialOptions...)

	if err != nil {
		log.Errorf("failed to connect, err %v", err)
		return err
	}

	client := pb.NewGatewayClient(conn)

	callContext, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	timer := time.NewTimer(time.Duration(c.Int(flags.Interval.Name)) * time.Second)

	stream, _ := client.NewTxs(callContext, &pb.TxsRequest{Filters: "", AuthHeader: c.String(flags.AuthHeader.Name)})

	for {
		select {
		case <-timer.C:
			file, err := os.Create("output.csv")
			if err != nil {
				log.Errorf("Error creating file %v", err)
				return err
			}
			defer file.Close()

			for receiveTime, txs := range cf.txsReceivedFromFeed {
				for _, tx := range txs {
					var ethTx types.Transaction
					err := ethTx.UnmarshalBinary(tx.RawTx)
					if err != nil {
						e := rlp.DecodeBytes(tx.RawTx, &ethTx)
						if e != nil {
							fmt.Println("error in decode tx ", err)
						}
					}
					entry := txEntry{feedTime: receiveTime, txTime: time.Unix(0, tx.Time)}
					cf.hashWithTimes[ethTx.Hash().String()] = entry
				}
			}

			for hash, entry := range cf.hashWithTimes {
				timeDiff := entry.feedTime.Sub(entry.txTime).Microseconds()
				timeLayout := "2006-01-02T15:04:05.999999999"
				feedTimeStr := entry.feedTime.Format(timeLayout)
				txTimeStr := entry.txTime.Format(timeLayout)
				line := fmt.Sprintf("%s %s %s %v\n", hash, feedTimeStr, txTimeStr, timeDiff)
				_, err := file.WriteString(line)
				if err != nil {
					log.Errorf("Error writing to file %v", err)
					return err
				}
			}
			return nil

		default:
			subscriptionNotification, err := stream.Recv()
			if err != nil {
				log.Errorf("error when receiving tx from feed %v", err)
				return err
			}
			cf.txsReceivedFromFeed[time.Now()] = subscriptionNotification.Tx
		}
	}

}
