package transactions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pb "github.com/BlockRazorinc/relay_example/protobuf"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"performance/internal/pkg/flags"
)

type BlockRazor struct {
	apiKey string
	uri    string
}

// defaultBlockRazorURL is the default BlockRazor URL for Frankfurt.
const defaultBlockRazorURL = "35.157.64.49:50051"

func NewBlockRazor(c *cli.Context, uri string) *BlockRazor {
	apiKey := c.String(flags.BlockRazorAPIKey.Name)
	if apiKey == "" {
		log.Fatalf("BlockRazor API key is required")
	}
	if uri == "" {
		uri = defaultBlockRazorURL
	}

	return &BlockRazor{
		apiKey: apiKey,
		uri:    uri,
	}
}

func (b *BlockRazor) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s %v", b.Name(), b.uri)

	auth := blockRazorAuth{apiKey: b.apiKey}

	size := 1024 * 1024 * 20 // 20MB

	c, err := grpc.NewClient(b.uri,
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(size),
		),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(&auth),
		grpc.WithWriteBufferSize(0),
		grpc.WithInitialConnWindowSize(128*1024))
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", b.Name(), err)
	}

	defer c.Close()

	client := pb.NewGatewayClient(c)

	// create a subscription using the stream-specific method and request
	stream, err := client.NewTxs(ctx, &pb.TxsRequest{NodeValidation: false})
	if err != nil {
		fmt.Println("failed to subscribe new tx: ", err)
		return
	}

	log.Infof("%s connection to %s established", b.Name(), b.uri)

	var reply *pb.TxsReply

	for {
		reply, err = stream.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Fatalf("failed to receive a message from %s: %v", b.Name(), err)
		}

		timeReceived := time.Now()

		msg := &Message{
			RawTx:            reply.Tx.RawTx,
			FeedReceivedTime: timeReceived,
			Size:             len(reply.Tx.RawTx),
		}
		out <- msg
	}
}

func (b *BlockRazor) ParseMessage(message *Message) (*Transaction, error) {
	tx := &types.Transaction{}
	err := rlp.DecodeBytes(message.RawTx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to decode raw transaction: %v", err)
	}

	return newTransaction(*tx, b.Name())
}

func (b *BlockRazor) Name() string {
	return "BlockRazorTransactionsGRPC"
}

type blockRazorAuth struct {
	apiKey string
}

func (a *blockRazorAuth) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"apiKey": a.apiKey}, nil
}

func (a *blockRazorAuth) RequireTransportSecurity() bool {
	return false
}
