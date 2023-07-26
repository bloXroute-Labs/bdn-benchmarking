package cmpfeeds

import "time"

type handler func() error

type message struct {
	hash         string
	bytes        []byte
	err          error
	timeReceived time.Time
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

type bxBkTx struct {
	From     string `json:"from"`
	Gas      string `json:"gas"`
	GasPrice string `json:"gasPrice"`
	Hash     string `json:"hash"`
	Input    string `json:"input"`
	Nonce    string `json:"nonce"`
	Value    string `json:"value"`
	//V        string `json:"v"`
	//R        string `json:"r"`
	//S        string `json:"s"`
	To string `json:"to"`
}

type txAdditionalFields struct {
	Index             string `json:"index"`
	GasUsed           string `json:"gasUsed"`
	CumulativeGasUsed string `json:"cumulativeGasUsed"`
	Type              string `json:"type"`
}

type bkHeader struct {
	Number string `json:"number"`
}

type bxBkFeedResponseWithTx struct {
	Params struct {
		Result struct {
			Hash         string   `json:"hash"`
			Header       bkHeader `json:"header"`
			Transactions []bxBkTx `json:"transactions"`
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

type txFiletInfo struct {
	tx               bxBkTx
	additionalFields txAdditionalFields
	txTrace          txTrace
	isPrivate        bool
	privateTxTime    string
	timestamp        time.Time
}
