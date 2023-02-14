package ws

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"net/http"
	"strings"
)

// Request represents data which is needed to send RPC requests to ETH node or BX gateway.
type Request struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type subscribeResponse struct {
	Error  map[string]interface{} `json:"error"`
	Result string                 `json:"result"`
}

// Connection is a thin wrapper around websocket connection which provides convenience methods
// for subscribing a feed or making an RPC call.
type Connection struct {
	conn *websocket.Conn
}

// SubscribeTxFeedEth subscribes to the ETH node feed.
func (c *Connection) SubscribeTxFeedEth(id int) (*Subscription, error) {
	return c.subscribe(newSubTxFeedRequestEth(id), eth)
}

// SubscribeTxFeedBX subscribes to BX gateway feed.
func (c *Connection) SubscribeTxFeedBX(
	id int,
	feedName string,
	excTxContents bool,
	duplicates bool,
	includeFromBlockchain bool,
	useLightGateway bool,
) (*Subscription, error) {
	return c.subscribe(
		newSubTxFeedRequestBX(
			id,
			feedName,
			excTxContents,
			duplicates,
			includeFromBlockchain,
			useLightGateway),
		bx)
}

// SubscribeBkFeedEth subscribes to the Eth node feed.
func (c *Connection) SubscribeBkFeedEth(id int) (*Subscription, error) {
	return c.subscribe(newSubBkFeedRequestEth(id), eth)
}

// SubscribeBkFeedBX subscribes to the BX gateway feed.
func (c *Connection) SubscribeBkFeedBX(
	id int,
	feedName string,
	excBkContents bool,
) (*Subscription, error) {
	return c.subscribe(newSubBkFeedRequestBX(id, feedName, excBkContents), bx)
}

func (c *Connection) subscribe(req *Request, t subscriptionType) (*Subscription, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err = c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		return nil, err
	}

	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	var res subscribeResponse
	if err = json.Unmarshal(data, &res); err != nil {
		return nil, err
	}

	if res.Error != nil {
		return nil, fmt.Errorf("error from RPC: %v", res.Error)
	}

	return &Subscription{
		ID:   res.Result,
		Conn: c,
		Type: t,
	}, nil
}

// Call is a convenience method to make an RPC call.
func (c *Connection) Call(req *Request) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err = c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		return nil, err
	}

	_, data, err := c.conn.ReadMessage()

	return data, err
}

// Close closes a connection.
func (c *Connection) Close() error {
	if err := c.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	); err != nil {
		return err
	}

	return c.conn.Close()
}

// NewConnection creates and initializes a new websocket connection.
func NewConnection(uri, authToken string) (*Connection, error) {
	header := http.Header{}
	if authToken != "" {
		header.Add("Authorization", authToken)
	}

	tlsConfig := tls.Config{}
	if strings.Contains(uri, "wss") {
		tlsConfig.InsecureSkipVerify = true
	}
	dialer := websocket.Dialer{TLSClientConfig: &tlsConfig}

	conn, resp, err := dialer.Dial(uri, header)
	if err != nil {
		return nil, err
	}
	err = resp.Body.Close()

	return &Connection{
		conn: conn,
	}, err
}

type subscriptionType byte

const (
	bx  subscriptionType = 1
	eth subscriptionType = 2
)

// Subscription represents a subscription to a websocket feed.
type Subscription struct {
	ID   string
	Conn *Connection
	Type subscriptionType
}

// Unsubscribe unsubscribes from the feed.
func (s *Subscription) Unsubscribe() error {
	switch s.Type {
	case bx:
		return s.unsubscribe(NewRequest(1, "unsubscribe", []interface{}{s.ID}))
	case eth:
		return s.unsubscribe(NewRequest(1, "eth_unsubscribe", []interface{}{s.ID}))
	}

	return fmt.Errorf("unknown subscription type: %d", s.Type)
}

func (s *Subscription) unsubscribe(req *Request) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	return s.Conn.conn.WriteMessage(websocket.TextMessage, body)
}

// NextMessage is a convenience method which reads and returns the next data item from the feed.
func (s *Subscription) NextMessage() ([]byte, error) {
	_, r, err := s.Conn.conn.NextReader()

	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

func newSubTxFeedRequestEth(id int) *Request {
	return NewRequest(id, "eth_subscribe", []interface{}{
		"newPendingTransactions",
	})
}

func newSubTxFeedRequestBX(
	id int,
	feedName string,
	excTxContents bool,
	duplicates bool,
	incFromBlockchain bool,
	useGoGateway bool,
) *Request {
	options := make(map[string]interface{})

	if !useGoGateway {
		options["duplicates"] = duplicates
		options["include_from_blockchain"] = incFromBlockchain
	}

	options["include"] = []string{}
	options["filter"] = ""

	return NewRequest(id, "subscribe", []interface{}{
		feedName, options,
	})
}

func newSubBkFeedRequestEth(id int) *Request {
	return NewRequest(id, "eth_subscribe", []interface{}{
		"newHeads",
	})
}

func newSubBkFeedRequestBX(id int, feedName string, excBkContents bool) *Request {
	options := make(map[string]interface{})

	if excBkContents {
		options["include"] = []string{"hash", "header"}
	} else {
		options["include"] = []string{"hash", "header", "transactions", "uncles"}
	}

	return NewRequest(id, "subscribe", []interface{}{
		feedName, options,
	})
}

// NewRequest is a convenience method to create a Request struct.
func NewRequest(id int, method string, params []interface{}) *Request {
	return &Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
}
