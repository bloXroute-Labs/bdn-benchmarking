package blocks

import (
	"context"
	"performance/internal/pkg/flags"
	"sync"
	"time"

	fiber "github.com/chainbound/fiber-go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type Fiber struct {
	authHeader string
	uri        string
}

const defaultFiberURL = "beta.fiberapi.io:8080"

func NewFiber(c *cli.Context, uri string) *Fiber {
	if uri == "" {
		uri = defaultFiberURL
	}
	return &Fiber{
		authHeader: c.String(flags.FiberAuthKey.Name),
		uri:        uri,
	}
}

func (f Fiber) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	defer wg.Done()
	log.Infof("Initiating connection to %s", f.Name())

	client := fiber.NewClient(f.uri, f.authHeader)
	defer client.Close()

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	ch := make(chan *fiber.ExecutionPayload)
	go func() {
		if err := client.SubscribeNewExecutionPayloads(ch); err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			log.Fatal(err)
		}
	}()

	log.Infof("connection to %s established", f.Name())

	for {
		select {
		case <-ctx.Done():
			log.Infof("stop %s feed", f.Name())
			return
		case block := <-ch:
			timeReceived := time.Now()

			blockHash := block.Header.Hash.String()
			msg := &Message{
				FeedReceivedTime: timeReceived,
				BlockHash:        blockHash,
			}
			out <- msg
		}
	}

}

func (f Fiber) ParseMessage(message *Message) (*Block, error) {
	return &Block{Hash: message.BlockHash}, nil
}

func (Fiber) Name() string {
	return "FiberBlocks"
}
