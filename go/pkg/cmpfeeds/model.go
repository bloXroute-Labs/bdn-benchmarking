package cmpfeeds

import "time"

type handler func() error

type message struct {
	hash              string
	bytes             []byte
	err               error
	timeReceived      time.Time
	gwMessageLen      int
	mevlinkMessageLen int
}

type hashEntry struct {
	ethTimeReceived     time.Time
	bxrTimeReceived     time.Time
	fiberTimeReceived   time.Time
	mevLinkTimeReceived time.Time
	hash                string
	gwMessageLen        int
	mevlinkMessageLen   int
}

type ethTxFeedResponse struct {
	Params struct {
		Subscription string `json:"subscription"`
		Result       string `json:"result"`
	} `json:"params"`
}

type ethBkFeedResponse struct {
	Params struct {
		Subscription string `json:"subscription"`
		Result       struct {
			Hash string `json:"hash"`
		} `json:"result"`
	} `json:"params"`
}

type bxTxFeedResponse struct {
	Params struct {
		Result struct {
			TxHash     string `json:"txHash"`
			TxContents struct {
				GasPrice *string `json:"gasPrice"`
				To       *string `json:"to"`
			} `json:"txContents"`
		} `json:"result"`
	} `json:"params"`
}

type bxBkFeedResponse struct {
	Params struct {
		Result struct {
			Hash         string                   `json:"hash"`
			Transactions []map[string]interface{} `json:"transactions"`
		} `json:"result"`
	} `json:"params"`
}

type ethTxContentsResponse struct {
	Result *struct {
		GasPrice string `json:"gasPrice"`
		To       string `json:"to"`
	} `json:"result"`
}

type ethBkContentsResponse struct {
	Result *struct {
		Hash string `json:"hash"`
	} `json:"result"`
}
