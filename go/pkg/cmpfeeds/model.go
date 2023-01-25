package cmpfeeds

import "time"

type handler func() error

type message struct {
	hash  string
	bytes []byte
	err   error
}

type hashEntry struct {
	ethTimeReceived time.Time
	bxrTimeReceived time.Time
	hash            string
}

type grpcHashEntry struct {
	grpcBxrTimeReceived time.Time
	bxrTimeReceived     time.Time
	hash                string
}

type grpcFeedResponse struct {
	Tx []struct {
		TxHash   string  `json:"hash"`
		GasPrice *string `json:"gas_price"`
		To       *string `json:"to"`
	} `json:"tx"`
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
			Hash string `json:"hash"`
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
