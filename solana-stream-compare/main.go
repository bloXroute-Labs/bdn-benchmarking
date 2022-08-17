package main

import (
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

type endpointNumType int

const (
	nodeEndpoint1 endpointNumType = iota
	nodeEndpoint2
)

type connType string

const (
	solanaNode  connType = "node"
	blxrGateway connType = "blxr"
)

type solanaSlot struct {
	Params struct {
		Result struct {
			Slot int `json:"slot"`
		} `json:"result"`
	} `json:"params"`
}

func (s *solanaSlot) slotNum(rawBlock []byte) (int, error) {
	err := json.Unmarshal(rawBlock, s)
	if err != nil {
		return -1, err
	}

	return s.Params.Result.Slot, nil
}

type update struct {
	source              endpointNumType
	rawBlock            []byte
	recvTime            time.Time
	solanaMessageUpdate solanaSlot
}

type entryInfo struct {
	endpoint1Time, endpoint2Time time.Time
	size                         float32
}

func (b *entryInfo) Upd(rt *update) {
	switch rt.source {
	case nodeEndpoint1:
		if b.endpoint1Time.IsZero() {
			b.endpoint1Time = rt.recvTime
			b.size = float32(len(rt.rawBlock))
		}
	case nodeEndpoint2:
		if b.endpoint2Time.IsZero() {
			b.endpoint2Time = rt.recvTime
			b.size = float32(len(rt.rawBlock))
		}
	default:
		// discard the update if both solana node and blxr data have already been populated
	}
}

type wsEndpoint struct {
	connectionType connType
	uri            string
	nodeNum        endpointNumType
}

type wsConnWithEndpointNum struct {
	conn    *websocket.Conn
	nodeNum endpointNumType
}

var (
	authToken    = flag.String("auth-header", "", "bloxroute authorization header")
	endpointURIs = flag.String(
		"endpoint-ws-uris",
		"",
		"comma separated list of two endpoints, "+
			"the endpoint can be either solana node ws endpoint or bloxroute solana services endpoint, "+
			"sample input: blxr+wss://virginia.solana.blxrbdn.com/ws,node+wss://api.mainnet-beta.solana.com",
	)
	blxrSubReq = flag.String(
		"blxr-sub-req",
		`{"id": 1, "method": "subscribe", "params": ["orca", {"include": []}]}`,
		"subscribe request used for bloxroute solana services endpoint",
	)
	seconds = flag.Int64("interval", 60, "benchmark duration")
	dumpAll = flag.Bool("dump-all", true, "dump receiving time data to a csv file")
)

func listen(wg *sync.WaitGroup, ch chan *update, seenMap map[int]*entryInfo) {
	defer wg.Done()
	var firstSeen = func(u *update) *entryInfo {
		switch u.source {
		case nodeEndpoint1:
			return &entryInfo{
				endpoint1Time: u.recvTime,
				size:          float32(len(u.rawBlock)),
			}
		case nodeEndpoint2:
			return &entryInfo{
				endpoint2Time: u.recvTime,
				size:          float32(len(u.rawBlock)),
			}
		default:
			return nil
		}
	}

	for recv := range ch {
		slotNum, err := recv.solanaMessageUpdate.slotNum(recv.rawBlock)
		if err != nil {
			log.Errorf("error parsing notification from: %d: %s", recv.source, err)
			continue
		}

		if slotNum == -1 {
			continue
		}

		seen, ok := seenMap[slotNum]
		if !ok {
			seenMap[slotNum] = firstSeen(recv)
			continue
		}

		seen.Upd(recv)
	}

}

func dumpFile(seenMap map[int]*entryInfo) error {
	csvFile, err := os.Create("BenchmarkOutput.csv")
	log.Printf("Dumping data to csv file BenchmarkOutput.csv")
	if err != nil {
		return err
	}
	defer func(csvFile *os.File) {
		err = csvFile.Close()
		if err != nil {
			log.Errorf("cannot close csv file, %v", err)
		}
	}(csvFile)

	w := csv.NewWriter(csvFile)
	row := []string{"slot", "endpoint1 time", "endpoint2 time", "diff"}
	if err = w.Write(row); err != nil {
		log.Errorf("error writing record to file, %v\n", err)
	}

	for key, entry := range seenMap {
		ep1T := "not received"
		ep2T := "not received"
		if !entry.endpoint1Time.IsZero() {
			ep1T = entry.endpoint1Time.Format("2006-01-02T15:04:05.000")
		}
		if !entry.endpoint2Time.IsZero() {
			ep2T = entry.endpoint2Time.Format("2006-01-02T15:04:05.000")
		}

		var diff time.Duration

		if !entry.endpoint1Time.IsZero() && !entry.endpoint2Time.IsZero() {
			diff = entry.endpoint2Time.Sub(entry.endpoint1Time)
		}

		row := []string{strconv.Itoa(key), ep1T, ep2T, strconv.FormatInt(diff.Milliseconds(), 10)}
		if err = w.Write(row); err != nil {
			log.Errorf("error writing record to file, %v\n", err)
		}
	}
	w.Flush()
	return err
}

func main() {
	flag.Parse()
	endpoints := strings.Split(*endpointURIs, ",")
	if len(endpoints) != 2 {
		log.Fatalln("invalid number of endpoints provided in endpoint-ws-uris, please provide two endpoints")
	}

	wsEndpointsList := make([]wsEndpoint, 0)

	for _, typeAndURI := range endpoints {
		var nodeNum endpointNumType

		typeAndURIParts := strings.Split(typeAndURI, "+")
		if len(typeAndURIParts) != 2 {
			log.Fatalln("invalid endpoint format in endpoint-ws-uris")
		}
		connectionTypeStr := typeAndURIParts[0]
		connectionURI := typeAndURIParts[1]
		switch connType(connectionTypeStr) {
		case solanaNode, blxrGateway:
		default:
			log.Fatalln("invalid connection type in endpoint-ws-uris")
		}

		if !strings.HasPrefix(connectionURI, "wss:") && !strings.HasPrefix(connectionURI, "ws:") {
			log.Fatalln("invalid WebSocket connection protocol in endpoint-ws-uris")
		}

		if len(wsEndpointsList) == 0 {
			nodeNum = nodeEndpoint1
		} else {
			nodeNum = nodeEndpoint2
		}

		endpoint := wsEndpoint{
			connectionType: connType(connectionTypeStr),
			uri:            connectionURI,
			nodeNum:        nodeNum,
		}
		wsEndpointsList = append(wsEndpointsList, endpoint)
	}

	var ctx, cancel = context.WithCancel(context.Background())
	var ch = make(chan *update, 1000)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		os.Exit(0)
	}()

	log.Printf("The benchmark will run for %v seconds", *seconds)

	timeToRun := time.Duration(*seconds * time.Second.Nanoseconds())

	var readerWG sync.WaitGroup
	readerWG.Add(2)
	var listenerWG sync.WaitGroup
	listenerWG.Add(1)

	seenSlots := map[int]*entryInfo{}

	connections := make([]wsConnWithEndpointNum, 0)
	for _, e := range wsEndpointsList {
		conn := subscribeToFeed(e.uri, e.connectionType)
		connWithNum := wsConnWithEndpointNum{
			conn:    conn,
			nodeNum: e.nodeNum,
		}
		connections = append(connections, connWithNum)
	}

	go listen(&listenerWG, ch, seenSlots)
	for _, c := range connections {
		go read(ctx, &readerWG, ch, c.conn, c.nodeNum)
	}

	fmt.Println("------------------------------------------------------------------")
	log.Infof("End time: %v", time.Now().Add(time.Duration(time.Second.Nanoseconds()**seconds)).String())

	time.Sleep(timeToRun)

	cancel()
	readerWG.Wait()
	close(ch)
	listenerWG.Wait()

	log.Infoln("Streaming finished, processing...")

	var totalBlocksFromEndpoint1, totalBlocksFromEndpoint2, totalFromBoth, totalDiffEndpoint1Nanos, totalDiffEndpoint2Nanos, fasterEndpoint1, fasterEndpoint2 int64

	if *dumpAll {
		err := dumpFile(seenSlots)
		if err != nil {
			log.Errorf("failed creating file %v", err)
		}
	}

	for _, entry := range seenSlots {
		if !entry.endpoint1Time.IsZero() {
			totalBlocksFromEndpoint1++
		}
		if !entry.endpoint2Time.IsZero() {
			totalBlocksFromEndpoint2++
		}

		if !entry.endpoint1Time.IsZero() && !entry.endpoint2Time.IsZero() {
			totalFromBoth++
			if entry.endpoint2Time.Before(entry.endpoint1Time) {
				fasterEndpoint2++
				totalDiffEndpoint2Nanos += entry.endpoint1Time.Sub(entry.endpoint2Time).Nanoseconds()
			} else {
				fasterEndpoint1++
				totalDiffEndpoint1Nanos += entry.endpoint2Time.Sub(entry.endpoint1Time).Nanoseconds()
			}
		}
	}

	fmt.Printf("Summary\n")
	fmt.Printf("Total number of slots seen: %v\n", len(seenSlots))
	fmt.Printf("Total slots from endpoint1[%s] : %v\n", wsEndpointsList[0].uri, totalBlocksFromEndpoint1)
	fmt.Printf("Total slots from endpoint2[%s] : %v\n", wsEndpointsList[1].uri, totalBlocksFromEndpoint2)
	fmt.Printf("Total slots received from both: %v\n", totalFromBoth)
	fmt.Printf("Number of slots received first from endpoint1[%s] : %v\n", wsEndpointsList[0].uri, fasterEndpoint1)
	fmt.Printf("Number of slots received first from endpoint2[%s] : %v\n", wsEndpointsList[1].uri, fasterEndpoint2)
	if totalFromBoth > 0 {
		fmt.Printf("Percentage of slots seen first from endpoint2: %0.2f\n", 100*float32(fasterEndpoint2)/float32(totalFromBoth))
	}

	if totalFromBoth != 0 {
		var endpoint2AverageTimeDif float32
		if fasterEndpoint2 != 0 {
			endpoint2AverageTimeDif = float32(totalDiffEndpoint2Nanos/1e6) / float32(fasterEndpoint2)
		}

		var endpoint1AverageTimeDif float32
		if fasterEndpoint1 != 0 {
			endpoint1AverageTimeDif = float32(totalDiffEndpoint1Nanos/1e6) / float32(fasterEndpoint1)
		}

		fmt.Printf("The average time difference for slots received first from endpoint1[%s] (ms): %0.2f\n", wsEndpointsList[0].uri, endpoint1AverageTimeDif)
		fmt.Printf("The average time difference for slots received first from endpoint2[%s] (ms): %0.2f\n", wsEndpointsList[1].uri, endpoint2AverageTimeDif)
		fmt.Printf("On average, endpoint2 is faster than endpoint1 with a time difference (ms): %0.2f\n", (float32(fasterEndpoint2)*endpoint2AverageTimeDif-float32(fasterEndpoint1)*endpoint1AverageTimeDif)/float32(totalFromBoth))
	}

}

func read(
	ctx context.Context,
	wg *sync.WaitGroup,
	ch chan *update,
	c *websocket.Conn,
	nodeType endpointNumType,
) {
	defer wg.Done()
	defer closeConnection(c)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, r, err := c.NextReader()
			if err != nil {
				log.Errorf("next reader, %v\n", err)
				continue
			}

			message, err := ioutil.ReadAll(r)
			if err != nil {
				log.Errorf("read error, %v\n", err)
				continue
			}

			ch <- &update{
				source:              nodeType,
				rawBlock:            message,
				recvTime:            time.Now(),
				solanaMessageUpdate: solanaSlot{},
			}
		}
	}
}

func subscribeToFeed(connectionURI string, endpointType connType) *websocket.Conn {
	header := http.Header{}
	var requestBody string

	if endpointType == blxrGateway {
		header.Add("Authorization", *authToken)
		requestBody = *blxrSubReq
	} else {
		requestBody = `{"id": 1, "jsonrpc":"2.0", "method": "slotSubscribe"}`
	}

	tlsConfig := tls.Config{}
	if strings.HasPrefix(connectionURI, "wss") {
		tlsConfig.InsecureSkipVerify = true
	}
	dialer := websocket.Dialer{TLSClientConfig: &tlsConfig}

	log.Printf("connecting to %s", connectionURI)
	c, resp, err := dialer.Dial(connectionURI, header)
	if err != nil {
		log.Fatalln("dial error, %v", err)
	}
	defer resp.Body.Close()

	// subscribe to feed
	err = c.WriteMessage(websocket.TextMessage, []byte(requestBody))
	if err != nil {
		log.Fatalln("failed to write message:", err)
	}

	_, response, err := c.ReadMessage()
	if err != nil {
		log.Fatalln(err)
	}

	err = checkThatConnectionSuccessfullyEstablished(response)
	if err != nil {
		log.Fatalln(err)
	}

	return c
}

func closeConnection(conn *websocket.Conn) {
	err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		log.Errorf("failed write close message to socket: %s", err)
	}
	err = conn.Close()
	if err != nil {
		log.Errorf("failed to close connection: %s", err)
	}
}

func checkThatConnectionSuccessfullyEstablished(response []byte) error {
	var rpcResponse map[string]interface{}
	err := json.Unmarshal(response, &rpcResponse)
	if err != nil {
		return err
	}

	rpcError, ok := rpcResponse["error"]
	if !ok {
		return nil
	}

	return fmt.Errorf("error from RPC: %v", rpcError)
}
