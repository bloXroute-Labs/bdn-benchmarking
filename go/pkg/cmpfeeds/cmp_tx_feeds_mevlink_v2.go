package cmpfeeds

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/gorilla/websocket"
	mlstreamer "github.com/mevlink/streamer-go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"net/http"
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"strconv"
	"time"
)

const timeLayout = "2006-01-02T15:04:05.000"

type MevlinkV2Compare struct{}

func NewMevlinkV2Compare() *MevlinkV2Compare {
	return &MevlinkV2Compare{}
}

func (m *MevlinkV2Compare) Run(c *cli.Context) error {
	log.Info("Starting")
	filePath := "all_transactions.csv"

	file, err := os.Create(filePath)
	if err != nil {
		fmt.Println("Failed to create file:", err)
		return err
	}
	defer file.Close()

	gwFeedData := map[time.Time][]byte{}
	mevlinkFeedData := map[time.Time][]byte{}
	ctx, cancel := context.WithCancel(context.Background())

	go readFeedFromGateway(ctx, c.String(flags.Gateway.Name), c.String(flags.AuthHeader.Name), gwFeedData)
	go readFeedFromMEVLink(ctx, c.String(flags.MEVLinkAPIKey.Name), c.String(flags.MEVLinkAPISecret.Name), mevlinkFeedData)

	duration := time.Second * time.Duration(c.Int(flags.Interval.Name))
	time.Sleep(duration)
	cancel()
	// to make sure that all streams ended
	time.Sleep(time.Second * 2)

	log.Info("Processing data from feeds")

	gatewayTransactions := parseGatewayData(gwFeedData)
	mevlinkTransactions := parseMevlinkData(mevlinkFeedData)

	uniqueTransactions := compareTransactions(mevlinkTransactions, gatewayTransactions)

	csvFile := csv.NewWriter(file)
	if err = csvFile.Write([]string{"TxHash", "BloXRoute Time", "MEVLink Time", "Time Diff"}); err != nil {
		log.Fatalf("failed to write CSV header: %v", err)
	}

	var totalTimeDiff int64 = 0

	for hash, timestamps := range uniqueTransactions {
		timeDiff := timestamps.Gateway.Sub(timestamps.Mevlink).Milliseconds()
		totalTimeDiff += timeDiff
		record := []string{hash, timestamps.Gateway.Format(timeLayout), timestamps.Mevlink.Format(timeLayout), strconv.FormatInt(timeDiff, 10)}
		if err = csvFile.Write(record); err != nil {
			log.Errorf("failed to wriute data to CSV: %v", err)
			continue
		}
	}

	csvFile.Flush()

	log.Info("Done")
	log.Infof("Gateway is faster then MEVLink by (ms): %v", float64(totalTimeDiff)/float64(len(uniqueTransactions)))

	return nil
}

func readFeedFromMEVLink(ctx context.Context, apiKey string, apiSecret string, result map[time.Time][]byte) {
	log.Info("Initiating connection to mevlink")

	str := mlstreamer.NewStreamer(apiKey, apiSecret, 1)
	//str.ConfigureStream(&mlstreamer.StreamConfig{IncludeHashes: true})
	str.OnTransaction(func(txb []byte, hash mlstreamer.NullableHash, noticed, propagated time.Time) {
		timeReceived := time.Now()
		result[timeReceived] = txb
	})

	go func() {
		<-ctx.Done()
		log.Info("Stop mevlink feed")
		str.Stop()
	}()

	if err := str.Stream(); err != nil {
		log.Error(err)
	}
}

func readFeedFromGateway(ctx context.Context, url, authHeader string, result map[time.Time][]byte) {
	log.Info("Initiating connection to gateway")

	dialer := websocket.DefaultDialer
	wsSubscriber, _, err := dialer.Dial(url, http.Header{"Authorization": []string{authHeader}})
	if err != nil {
		log.Fatalf("cannot establish connection to: %v", err)
	}

	subRequest := `{"id": 1, "method": "subscribe", "params": ["newTxs", {"include": ["raw_tx"]}]}`
	err = wsSubscriber.WriteMessage(websocket.TextMessage, []byte(subRequest))
	if err != nil {
		log.Fatalf("cannot subscribe to gateway feed: %v", err)
	}

	_, _, err = wsSubscriber.ReadMessage()
	if err != nil {
		log.Fatalf("failed to subscribe to gateway feed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			unsubscribeReq := ws.NewRequest(1, "unsubscribe", []interface{}{1})
			body, err := json.Marshal(unsubscribeReq)
			if err != nil {
				log.Errorf("cannot marshal unsubscribe request to gateway feed: %v", err)
				return
			}
			err = wsSubscriber.WriteMessage(websocket.TextMessage, []byte(body))
			if err != nil {
				log.Errorf("cannot unsubscribe from gateway feed: %v", err)
				return
			}

			if err := wsSubscriber.Close(); err != nil {
				log.Errorf("cannot close gateway feed: %v", err)
				return
			}

			log.Info("Stop gateway feed")
			return
		default:
			_, nextNotification, err := wsSubscriber.ReadMessage()
			if err != nil {
				log.Errorf("failed to get new notifications from gateway feed: %v", err)
				continue
			}

			timeReceived := time.Now()

			result[timeReceived] = nextNotification
		}

	}
}

type rawTXResponse struct {
	Params struct {
		Result struct {
			RawTx string `json:"rawTx"`
		} `json:"result"`
	} `json:"params"`
}

func parseGatewayData(result map[time.Time][]byte) map[string]time.Time {
	hashes := map[string]time.Time{}

	for timestamp, data := range result {
		rawTX := rawTXResponse{}
		err := json.Unmarshal(data, &rawTX)
		if err != nil {
			log.Fatalf("failed to unmarshall gateway raw tx: %v", err)
		}
		hashes[parseRawTX(rawTX.Params.Result.RawTx)] = timestamp
	}

	return hashes
}

func parseMevlinkData(result map[time.Time][]byte) map[string]time.Time {
	hashes := map[string]time.Time{}

	for timestamp, data := range result {
		hashes[parseRawTX(hexutil.Encode(data))] = timestamp
	}

	return hashes
}

func parseRawTX(txStr string) string {
	txBytes, err := DecodeHex(txStr)
	if err != nil {
		log.Fatalf("failed to decode mevlink hash: %v", err)
	}

	var ethTx types.Transaction
	err = ethTx.UnmarshalBinary(txBytes)
	if err != nil {
		// If UnmarshalBinary failed, we will try RLP in case user made mistake
		e := rlp.DecodeBytes(txBytes, &ethTx)
		if e != nil {
			log.Fatalf("could not decode Ethereum transaction: %v", err)
		}
	}

	return ethTx.Hash().String()
}

type transactionsTimestamp struct {
	Mevlink time.Time
	Gateway time.Time
}

func compareTransactions(mevlinkTransactions, gatewayTransaction map[string]time.Time) map[string]transactionsTimestamp {
	uniqueTransactions := map[string]transactionsTimestamp{}
	for hash, mevlinkTimestamp := range mevlinkTransactions {
		_, ok := uniqueTransactions[hash]
		if !ok {
			gatewayTimestamp, ok := gatewayTransaction[hash]
			if ok {
				uniqueTransactions[hash] = transactionsTimestamp{
					Mevlink: mevlinkTimestamp,
					Gateway: gatewayTimestamp,
				}
			}
		}
	}

	return uniqueTransactions
}
