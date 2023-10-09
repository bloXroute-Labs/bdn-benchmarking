package flags

import "github.com/urfave/cli/v2"

// CLI flags for ethcompare
var (
	BloxrouteTxFeedName = &cli.StringFlag{
		Name:  "bloxroute-tx-feed-name",
		Usage: "specify feed name, possible values: 'newTxs', 'pendingTxs', 'transactionStatus'",
		Value: "newTxs",
	}
	Interval = &cli.IntFlag{
		Name:  "interval",
		Usage: "length of feed sample interval in seconds",
		Value: 60,
	}
	NumIntervals = &cli.IntFlag{
		Name:  "num-intervals",
		Usage: "number of intervals",
		Value: 1,
	}
	LeadTime = &cli.IntFlag{
		Name:  "lead-time",
		Usage: "seconds to wait before starting to compare feeds",
		Value: 60,
	}
	TrailTime = &cli.IntFlag{
		Name:  "trail-time",
		Usage: "seconds to wait after interval to receive tx on both feeds",
		Value: 60,
	}
	Dump = &cli.StringFlag{
		Name:  "dump",
		Usage: "specify info to dump, possible values: 'ALL', 'MISSING', 'ALL,MISSING'",
	}
	IgnoreDelta = &cli.IntFlag{
		Name:  "ignore-delta",
		Usage: "ignore tx with delta above this amount (seconds)",
		Value: 5,
	}
	BloxrouteAuthHeader = &cli.StringFlag{
		Name:  "bloxroute-auth-header",
		Usage: "authorization header created with account id and secret hash for bloxroute feeds authorization",
	}
	FiberAuthKey = &cli.StringFlag{
		Name:  "fiber-auth-key",
		Usage: "Fiber auth key",
	}
	MEVLinkAPIKey = &cli.StringFlag{
		Name:  "mevLink-api-key",
		Usage: "mevLink api key",
	}
	MEVLinkAPISecret = &cli.StringFlag{
		Name:  "mevLink-api-secret",
		Usage: "mevLink api secret",
	}
	NetworkNumber = &cli.IntFlag{
		Name:  "network-num",
		Usage: "network number, 1 for ETH(default), 56 for BSC",
		Value: 1,
	}
	FirstFeed = &cli.StringFlag{
		Name:     "first-feed",
		Usage:    "first feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC",
		Required: true,
	}
	SecondFeed = &cli.StringFlag{
		Name:     "second-feed",
		Usage:    "second feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC",
		Required: true,
	}
	FirstFeedURI = &cli.StringFlag{
		Name:  "first-feed-uri",
		Usage: "first feed uri",
	}
	SecondFeedURI = &cli.StringFlag{
		Name:  "second-feed-uri",
		Usage: "second feed uri",
	}
	BlockFeedURI = &cli.StringFlag{
		Name:  "block-feed-uri",
		Usage: "uri for block feed(gw/cloud-api)",
		Value: "ws://127.0.0.1:28333/ws",
	}
	ExcludeTxContent = &cli.BoolFlag{
		Name:  "exclude-tx-contents",
		Usage: "compare txs only with hash",
		Value: false,
	}
)
