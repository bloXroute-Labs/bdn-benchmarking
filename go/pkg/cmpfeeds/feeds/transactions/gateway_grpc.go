package transactions

import (
	"context"
	"fmt"
	"performance/internal/pkg/flags"
	"performance/pkg/constant"
	"sync"
	"time"

	pb "github.com/bloXroute-Labs/gateway/v2/protobuf"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type GatewayGRPC struct {
	uri       string
	c         *cli.Context
	enableTLS bool
}

const defaultGatewayGRPCURI = "127.0.0.1:5002"

func NewGatewayGRPC(c *cli.Context, uri string, enableTLS bool) *GatewayGRPC {
	if uri == "" {
		uri = defaultGatewayGRPCURI
	}
	return &GatewayGRPC{uri: uri, c: c, enableTLS: enableTLS}
}

func (g GatewayGRPC) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()

	log.Infof("Initiating connection to %s %v", g.Name(), g.uri)

	dialOptions := []grpc.DialOption{
		grpc.WithInitialWindowSize(constant.WindowSize),
	}

	if g.enableTLS {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")))
	} else {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(g.uri, dialOptions...)
	if err != nil {
		log.Fatalf("failed to connect %s: %v", g.Name(), err)
	}
	client := pb.NewGatewayClient(conn)

	//TODO: add filters
	stream, err := client.NewTxs(ctx, &pb.TxsRequest{Filters: "", AuthHeader: g.c.String(flags.BloxrouteAuthHeader.Name)})
	if err != nil {
		log.Fatalf("could not create %s: %v", g.Name(), err)
	}

	log.Infof("%s connection to %s established", g.Name(), g.uri)

	defer func() {
		if err := stream.CloseSend(); err != nil {
			log.Errorf("failed to close %s stream: %v", g.Name(), err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			log.Infof("stop %s feed", g.Name())
			return
		default:
			data, err := stream.Recv()
			timeReceived := time.Now()

			if err != nil {
				if ctx.Err() == context.Canceled {
					return
				}
				grpcErrorStatus := status.Convert(err)
				log.Errorf("can not receive new message from %s feed: %v, grpc code: %s", g.Name(), err, grpcErrorStatus.Code().String())
				continue
			}

			if data != nil {
				for _, tx := range data.Tx {
					out <- &Message{
						FeedReceivedTime: timeReceived,
						RawTx:            tx.RawTx,
						Size:             proto.Size(tx),
					}

				}
			}
		}
	}
}

func (g GatewayGRPC) ParseMessage(message *Message) (*Transaction, error) {
	var ethTx types.Transaction
	err := ethTx.UnmarshalBinary(message.RawTx)
	if err != nil {
		// If UnmarshalBinary failed, we will try RLP in case user made mistake
		e := rlp.DecodeBytes(message.RawTx, &ethTx)
		if e != nil {
			log.Errorf("could not decode %s transaction: %v", g.Name(), err)
			return nil, err
		}
	}

	return newTransaction(ethTx, g.Name())
}

func (g GatewayGRPC) Name() string {
	return fmt.Sprintf("GatewayTransactionsGRPC(%s)", g.uri)
}
