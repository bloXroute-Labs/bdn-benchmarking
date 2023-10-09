package transactions

import (
	"encoding/hex"
	"math/big"
	"math/rand"
	"strings"
	"time"

	pb "github.com/bloXroute-Labs/gateway/v2/protobuf"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	log "github.com/sirupsen/logrus"
)

// decodeHex gets the bytes of a hexadecimal string, with or without its `0x` prefix
func decodeHex(str string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(str, "0x"))
}

func generateTxs(networkNum, count int) []string {
	var txs []string
	for i := 0; i < count; i++ {
		accountAddr := common.HexToAddress("0xBBDEf5f330F08aFd93A7696f4ea79AF4a41D0f80")
		privateKeyHex := ""

		nonce := uint64(randomNumber())
		gasPrice := big.NewInt(3000000000)
		gasLimit := uint64(21000)
		chainID := big.NewInt(int64(networkNum))

		tx := types.NewTransaction(
			nonce,
			accountAddr,
			big.NewInt(0),
			gasLimit,
			gasPrice,
			nil,
		)

		privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
		if err != nil {
			log.Fatal(err)
		}
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
		if err != nil {
			log.Fatal(err)
		}

		rawTxBytes, err := signedTx.MarshalBinary()
		if err != nil {
			log.Fatal(err)
		}

		rawTxHex := hex.EncodeToString(rawTxBytes)
		txs = append(txs, rawTxHex)
	}
	return txs
}

func generateTxsWithSender(networkNum, count int, sender string) []*pb.TxAndSender {
	senderBytes, err := hex.DecodeString(sender)
	if err != nil {
		log.Fatal(err)
	}

	var txsWithSender []*pb.TxAndSender
	txs := generateTxs(networkNum, count)

	for _, tx := range txs {

		txWithSender := &pb.TxAndSender{
			Transaction: tx,
			Sender:      senderBytes,
		}
		txsWithSender = append(txsWithSender, txWithSender)
	}

	return txsWithSender
}

func randomNumber() int {
	rand.Seed(time.Now().UnixNano())
	return rand.Intn(20001)
}
