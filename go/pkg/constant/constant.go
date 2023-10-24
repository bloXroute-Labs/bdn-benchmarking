package constant

import "errors"

const (
	WindowSize = 128 * 1024
	MapSize    = 24000
)

var EmptyResponseFromGeth = errors.New("got empty response from geth")
