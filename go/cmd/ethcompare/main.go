package main

import (
	"os"
	"performance/internal/pkg/flags"
	"performance/internal/pkg/logger"
	"performance/pkg/cmpfeeds"
	"performance/pkg/cmptxspeed"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
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
					flags.Eth,
					flags.TxFeedName,
					flags.MinGasPrice,
					flags.Addresses,
					flags.ExcludeTxContents,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TrailTime,
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
				Name:  "txfilter",
				Usage: "filters transactions coming from BX gateway based on addresses",
				Flags: []cli.Flag{
					flags.Gateway,
					flags.BkFeedName,
					flags.ExcludeBkContents,
					flags.Dump,
					flags.UseCloudAPI,
					flags.NumIntervals,
					flags.AuthHeader,
					flags.CloudAPIWSURI,
					flags.Interval,
					flags.TrailTime,
				},
				Action: func(context *cli.Context) error {
					var intervalGroup sync.WaitGroup
					numIntervals := context.Int(flags.NumIntervals.Name)
					intervalGroup.Add(numIntervals)
					for i := 0; i < numIntervals; i++ {
						// create logger
						logrus := logger.NewLogger()

						logrus.Infof("running interval %v", i+1)
						// Get the time until the next midnight
						now := time.Now()
						nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, time.UTC)
						secondsUntilMidnight := int(nextMidnight.Sub(now).Seconds())
						flags.Interval.Value = secondsUntilMidnight
						logrus.Infof("This interval will run for %v seconds", secondsUntilMidnight)

						// This is a workaround so that two consequent intervals will use different filters
						// And we need that in order to avoid "duplicate feed request" to the gateway
						if i%2 == 0 {
							flags.ExcludeBkContents.Value = true
						} else {
							flags.ExcludeBkContents.Value = false
						}
						go func(i int, wg *sync.WaitGroup) {
							defer wg.Done()
							err := cmpfeeds.NewTxFilterService().Run(context, i+1, logrus)
							if err != nil {
								logrus.Fatalf("Error while running interval %v", i)
							}
						}(i, &intervalGroup)
						time.Sleep(time.Second * time.Duration(flags.Interval.Value))
					}
					intervalGroup.Wait()
					return nil
				},
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
			{
				Name:  "txwsgrpc",
				Usage: "compares stream of txs from gateway grpc vs gateway websocket",
				Flags: []cli.Flag{
					flags.Gateway,
					flags.GatewayGrpc,
					flags.TxFeedName,
					flags.MinGasPrice,
					flags.Addresses,
					flags.ExcludeTxContents,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TrailTime,
					flags.Dump,
					flags.ExcludeDuplicates,
					flags.TxIgnoreDelta,
					flags.UseCloudAPI,
					flags.Verbose,
					flags.ExcludeFromBlockchain,
					flags.AuthHeader,
					flags.UseGoGateway,
				},
				Action: cmpfeeds.NewTxWsGrpcCompareService().Run,
			},
			{
				Name:  "blockwsgrpc",
				Usage: "compares stream of blocks from gateway grpc vs gateway websocket",
				Flags: []cli.Flag{
					flags.Gateway,
					flags.GatewayGrpc,
					flags.BkFeedName,
					flags.Interval,
					flags.NumIntervals,
					flags.LeadTime,
					flags.TrailTime,
					flags.Dump,
					flags.UseCloudAPI,
					flags.Verbose,
					flags.ExcludeBkContents,
					flags.AuthHeader,
				},
				Action: cmpfeeds.NewBlockWsGrpcCompareService().Run,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
