package blocks

import "time"

type BXBkFeedResponse struct {
	Params struct {
		Result struct {
			Hash         string           `json:"hash"`
			Transactions []map[string]any `json:"transactions"`
		} `json:"result"`
	} `json:"params"`
}

type Block struct {
	Hash string
}

type Message struct {
	RawBlock         []byte
	FeedReceivedTime time.Time
	BlockHash        string
}
