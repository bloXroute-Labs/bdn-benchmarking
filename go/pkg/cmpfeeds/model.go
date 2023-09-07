package cmpfeeds

import "time"

type handler func() error

type nonceSenderEntry struct {
	nonce                  uint64
	sender                 string
	firstFeedTimeReceived  time.Time
	secondFeedTimeReceived time.Time
	firstFeedTXHash        string
	secondFeedTXHash       string
	firstFeedMessageSize   int
	secondFeedMessageSize  int
}

type blockEntry struct {
	firstFeedTimeReceived  time.Time
	secondFeedTimeReceived time.Time
}
