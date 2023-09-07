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
					flags.TxFeedName,
					flags.MinGasPrice,
					flags.Addresses,
					flags.ExcludeTxContents,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TxTrailTime,
					flags.Dump,
					flags.ExcludeDuplicates,
					flags.TxIgnoreDelta,
					flags.UseCloudAPI,
					flags.ExcludeFromBlockchain,
					flags.CloudAPIWSURI,
					flags.AuthHeader,
					flags.NodeWSEndpoint,
					flags.BXEndpoint,
					flags.BXAuthHeader,
					flags.SenderPrivateKey,
					flags.ChainID,
					flags.NumTxGroups,
					flags.GasPrice,
					flags.Delay,
					flags.FiberUri,
					flags.FiberAPIKey,
					flags.MEVLinkAPIKey,
					flags.MEVLinkAPISecret,
					flags.FiberAuthKey,
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
					flags.TxFeedName,
					flags.MinGasPrice,
					flags.Addresses,
					flags.ExcludeTxContents,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TxTrailTime,
					flags.Dump,
					flags.ExcludeDuplicates,
					flags.TxIgnoreDelta,
					flags.UseCloudAPI,
					flags.ExcludeFromBlockchain,
					flags.CloudAPIWSURI,
					flags.AuthHeader,
					flags.NodeWSEndpoint,
					flags.BXEndpoint,
					flags.BXAuthHeader,
					flags.SenderPrivateKey,
					flags.ChainID,
					flags.NumTxGroups,
					flags.GasPrice,
					flags.Delay,
					flags.FiberUri,
					flags.FiberAPIKey,
					flags.MEVLinkAPIKey,
					flags.MEVLinkAPISecret,
					flags.FiberAuthKey,
					flags.NetworkNumber,
					flags.FirstFeed,
					flags.SecondFeed,
					flags.FirstFeedURI,
					flags.SecondFeedURI,
					flags.BlockFeedURI,
				},
				Action: cmpfeeds.NewCompareBlocksService().Run,
			},
			{
				Name:  "feed-latency",
				Usage: "compare the time the tx send and the time tx received in the feed",
				Flags: []cli.Flag{
					flags.FirstFeedURI,
					flags.AuthHeader,
					flags.Interval,
				},
				Action: cmpfeeds.NewCompareFeedTransactions().Run,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
