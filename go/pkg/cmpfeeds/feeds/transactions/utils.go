package transactions

import (
	"encoding/hex"
	"strings"
)

// decodeHex gets the bytes of a hexadecimal string, with or without its `0x` prefix
func decodeHex(str string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(str, "0x"))
}
