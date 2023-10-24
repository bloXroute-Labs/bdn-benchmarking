package blocks

import (
	"context"
	"encoding/json"
	"fmt"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/ws"
	"strconv"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

type GatewayWS struct {
	uri        string
	authHeader string
}

const defaultGatewayWSURI = "ws://127.0.0.1:28333/ws"

func NewGatewayWS(c *cli.Context, uri string) *GatewayWS {
	if uri == "" {
		if c.IsSet(flags.BlockFeedURI.Name) {
			uri = c.String(flags.BlockFeedURI.Name)
		} else {
			uri = defaultGatewayWSURI
		}
	}
	return &GatewayWS{
		uri:        uri,
		authHeader: c.String(flags.BloxrouteAuthHeader.Name),
	}
}

func (g GatewayWS) Receive(ctx context.Context, wg *sync.WaitGroup, out chan *Message) {
	const feedName = "bdnBlocks"
	defer wg.Done()

	log.Infof("Initiating connection to %s %v", g.Name(), g.uri)

	conn, err := ws.NewConnection(g.uri, g.authHeader)
	if err != nil {
		log.Errorf("cannot establish connection to %s: %v", g.uri, err)
		return
	}

	log.Infof("%s connection to %s established", g.Name(), g.uri)

	defer func() {
		if err := conn.Close(); err != nil {
			log.Errorf("cannot close socket connection to %s %s: %v", g.Name(), g.uri, err)
		}
	}()

	sub, err := conn.SubscribeBkFeedBX(1, feedName, false)

	if err != nil {
		log.Errorf("cannot subscribe to feed %s %s: %v", g.Name(), feedName, err)
		return
	}

	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Errorf("cannot unsubscribe from feed %s %s: %v", g.Name(), feedName, err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Infof("stop %s feed", g.Name())
			return
		default:
			data, err := sub.NextMessage()
			if err != nil {
				log.Errorf("failed to get new message from feed %s %s: %v", g.Name(), feedName, err)
				continue
			}

			out <- &Message{
				RawBlock: data,
			}
		}
	}
}

func (g GatewayWS) ParseMessage(message *Message) (*Block, error) {
	var msg BXBkFeedResponse
	if err := json.Unmarshal(message.RawBlock, &msg); err != nil {
		log.Errorf("failed to unmarshal ws block message: %v, from %s feed", err, g.uri)
		return nil, err
	}

	return &Block{Hash: msg.Params.Result.Hash}, nil
}

func (g GatewayWS) ParseMessageToHash(message *Message) ([]string, error) {
	var msg BXBkFeedResponse
	if err := json.Unmarshal(message.RawBlock, &msg); err != nil {
		log.Errorf("failed to unmarshal ws block message: %v, from %s feed", err, g.uri)
		return nil, err
	}

	h := msg.Params.Result.Hash
	log.Debugf("got message at %s (BXR node, ALL), hash: %s", message.FeedReceivedTime, h)

	var hashes []string
	for _, v := range msg.Params.Result.Transactions {
		hashes = append(hashes, v["hash"].(string))
	}
	return hashes, nil
}

func (g GatewayWS) ParseMessageToSenderWithNonce(message *Message) ([]string, error) {
	var msg BXBkFeedResponse
	if err := json.Unmarshal(message.RawBlock, &msg); err != nil {
		log.Errorf("failed to unmarshal ws block message: %v, from %s feed", err, g.uri)
		return nil, err
	}

	h := msg.Params.Result.Hash
	log.Debugf("got message at %s (BXR node, ALL), hash: %s", message.FeedReceivedTime, h)

	var senderWithNonce []string
	for _, v := range msg.Params.Result.Transactions {
		nonce, err := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(v["nonce"]), "0x"), 16, 64)
		if err != nil {
			log.Errorf("can not parse nonce from %s feed: %v", g.Name(), err)
			continue
		}

		senderWithNonce = append(senderWithNonce, strings.ToLower(fmt.Sprint(v["from"]))+strconv.FormatUint(nonce, 10))
	}

	return senderWithNonce, nil
}

func (GatewayWS) Name() string {
	return "GatewayBlocksWS"
}
