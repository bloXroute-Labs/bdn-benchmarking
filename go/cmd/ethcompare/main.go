package main

import (
	"github.com/urfave/cli/v2"
	"log"
	"os"
	"performance/internal/pkg/flags"
	"performance/pkg/cmpfeeds"
	"performance/pkg/cmptxspeed"
)

func main() {
	app := &cli.App{
		Name:  "ethcompare",
		Usage: "compares stream of txs/blocks from gateway vs node",
		Commands: []*cli.Command{
			{
				Name:  "transactions",
				Usage: "compares stream of txs from gateway vs node",
				Flags: []cli.Flag{
					flags.Gateway,
					flags.Gateway2,
					flags.Eth,
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
					flags.Verbose,
					flags.ExcludeFromBlockchain,
					flags.CloudAPIWSURI,
					flags.AuthHeader,
					flags.UseGoGateway,
				},
				Action: cmpfeeds.NewTxFeedsCompareService().Run,
			},
			{
				Name:  "blocks",
				Usage: "compares stream of blocks from gateway vs node",
				Flags: []cli.Flag{
					flags.Gateway,
					flags.Eth,
					flags.BkFeedName,
					flags.ExcludeBkContents,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.BkTrailTime,
					flags.Dump,
					flags.BkIgnoreDelta,
					flags.UseCloudAPI,
					flags.AuthHeader,
					flags.CloudAPIWSURI,
				},
				Action: cmpfeeds.NewBkFeedsCompareService().Run,
			},
			{
				Name: "txspeed",
				Usage: "compares sending tx speed by submitting conflicting txs with the same nonce " +
					"to node and gateway, so only one tx will land on chain",
				Flags: []cli.Flag{
					flags.NodeWSEndpoint,
					flags.BXEndpoint,
					flags.BXAuthHeader,
					flags.SenderPrivateKey,
					flags.ChainID,
					flags.NumTxGroups,
					flags.GasPrice,
					flags.Delay,
				},
				Action: cmptxspeed.NewTxSpeedCompareService().Run,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
