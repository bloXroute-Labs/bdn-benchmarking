package main

import (
	"log"
	"os"
	"performance/internal/pkg/flags"
	"performance/pkg/cmpfeeds"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "compare",
		Usage: "compares stream of txs/blocks for 2 different feeds",
		Commands: []*cli.Command{
			{
				Name:  "transactions",
				Usage: "compares stream of txs from different feeds(Mevlink, Fiber, GatewayWS, GatewayGRPC)",
				Flags: []cli.Flag{
					flags.BloxrouteTxFeedName,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TrailTime,
					flags.Dump,
					flags.IgnoreDelta,
					flags.BloxrouteAuthHeader,
					flags.FiberAuthKey,
					flags.MEVLinkAPIKey,
					flags.MEVLinkAPISecret,
					flags.NetworkNumber,
					flags.FirstFeed,
					flags.SecondFeed,
					flags.FirstFeedURI,
					flags.SecondFeedURI,
					flags.BlockFeedURI,
				},
				Action: cmpfeeds.NewCompareTransactionsService().Run,
			},
			{
				Name:  "blocks",
				Usage: "compares stream of blocks from different feeds(Mevlink, Fiber, GatewayWS, GatewayGRPC)",
				Flags: []cli.Flag{
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TrailTime,
					flags.Dump,
					flags.IgnoreDelta,
					flags.BloxrouteAuthHeader,
					flags.FiberAuthKey,
					flags.FirstFeed,
					flags.SecondFeed,
					flags.FirstFeedURI,
					flags.SecondFeedURI,
				},
				Action: cmpfeeds.NewCompareBlocksService().Run,
			},
			{
				Name:  "feed-latency",
				Usage: "compare the time the tx send and the time tx received in the feed",
				Flags: []cli.Flag{
					flags.FirstFeedURI,
					flags.BloxrouteAuthHeader,
					flags.Interval,
				},
				Action: cmpfeeds.NewCompareFeedTransactions().Run,
			},
			{
				Name:  "sending-batch",
				Usage: "compare the sending time of a batch of txs",
				Flags: []cli.Flag{
					flags.BloxrouteAuthHeader,
					flags.BloxrouteTxFeedName,
					flags.FirstFeed,
					flags.SecondFeed,
					flags.FirstFeedURI,
					flags.SecondFeedURI,
				},
				Action: cmpfeeds.NewCompareSendingTransactionsService().Run,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
