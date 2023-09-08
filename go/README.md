# Public benchmarking scripts (Go)

## Objective
This package contains a command line utility which is intended for benchmarking
performance of source A vs source B. It has the following functionality:
* Compare transaction streams.
* Compare block streams.
* Compare feed latency.

## Usage
This command line 1 top level command:
* `compare` - compares stream of `transactions` or `blocks` from source A vs source B. And `feed-latency`.
Available subcommands:
* `transactions` - compares transaction streams.
* `blocks` - compares block streams.
* `feed-latency` - compares feed latency.

### Transactions streams compare
This benchmark is invoked by `compare transactions` command which has the following options:
```
   --first-feed               first feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC
   --second-feed              second feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC 
   --first-feed-uri           first feed uri (For Bloxroute Gateway GRPC default: 127.0.0.1:5002, for Fiber: beta.fiberapi.io:8080)
   --second-feed-uri          second feed uri (For Bloxroute Gateway GRPC default: 127.0.0.1:5002, for Fiber: beta.fiberapi.io:8080)
   --bloxroute-auth-header    authorization header created with account id and secret hash for bloxroute feeds authorization
   --fiber-auth-key           fiber auth key
   --mevlink-api-key          mevlink api key
   --mevkink-api-secret       mevlink api secret
   --block-feed-uri           block feed uri (to check that transactions are in blocks) (default is ws://127.0.0.1:28333/ws)
   --interval                 length of feed sample interval in seconds(default: 60sec)
   --num-intervals            number of intervals(default: 1)
   --lead-time                seconds to wait before starting to compare feeds(default: 60sec)
   --trail-time               seconds to wait after interval to receive blocks on both feeds(default: 60sec)
   --dump                     specify info to dump, possible values: 'ALL', 'MISSING', 'ALL,MISSING'
   --ignore-delta             ignore blocks with delta above this amount (seconds) (default: 5)
   --help, -h                 show help (default: false)
```

#### Example
Here is an example of using `transactions` command:
The following command can be used to print help related to `blocks` command:
```shell
go run cmd/ethcompare/main.go transactions -h
```
Gateway GRPC vs Gateway WS:
```shell
go run cmd/ethcompare/main.go transactions --bloxroute-auth-header <bloxroute-auth-header> --first-feed GatewayGRPC --second-feed GatewayWS --first-feed-uri 127.0.0.1:5002 --second-feed-uri ws://127.0.0.1:28333/ws --block-feed-uri ws://127.0.0.1:28334/ws --trail-time 10 --interval 180  --dump "ALL,MISSING"  
```
Gateway GRPC vs Fiber:
```shell
go run cmd/ethcompare/main.go transactions --bloxroute-auth-header <bloxroute-auth-header> --fiber-auth-key <fiber-auth-key> --first-feed GatewayGRPC --second-feed Fiber --first-feed-uri 127.0.0.1:5002 --block-feed-uri ws://127.0.0.1:28334/ws --trail-time 10 --interval 180  --dump "ALL,MISSING"  
```
Gateway GRPC vs Mevlink:
```shell
go run cmd/ethcompare/main.go transactions --bloxroute-auth-header <bloxroute-auth-header> --mevLink-api-key <mevlink-api-key> --mevLink-api-secret <mevlink-api-secret> --first-feed GatewayGRPC --second-feed Mevlink --first-feed-uri 127.0.0.1:5002 --block-feed-uri ws://127.0.0.1:28334/ws --trail-time 10 --interval 180  --dump "ALL,MISSING"  
```

### Blocks streams compare
This benchmark is invoked by `compare blocks` command which has the following options:
```
   --first-feed               first feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC
   --second-feed              second feed to compare, can be: Mevlink, Fiber, GatewayWS, GatewayGRPC 
   --first-feed-uri           first feed uri (For Bloxroute Gateway GRPC default: 127.0.0.1:5002, for Fiber: beta.fiberapi.io:8080)
   --second-feed-uri          second feed uri (For Bloxroute Gateway GRPC default: 127.0.0.1:5002, for Fiber: beta.fiberapi.io:8080)
   --bloxroute-auth-header    authorization header created with account id and secret hash for bloxroute feeds authorization
   --fiber-auth-key           fiber auth key
   --interval                 length of feed sample interval in seconds(default: 60sec)
   --num-intervals            number of intervals(default: 1)
   --lead-time                seconds to wait before starting to compare feeds(default: 60sec)
   --trail-time               seconds to wait after interval to receive blocks on both feeds(default: 60sec)
   --dump                     specify info to dump, possible values: 'ALL', 'MISSING', 'ALL,MISSING'
   --ignore-delta             ignore blocks with delta above this amount (seconds) (default: 5)
   --help, -h                 show help (default: false)
```

The following command can be used to print help related to `blocks` command:
```shell
go run cmd/ethcompare/main.go blocks -h
```
#### Example
Here is an example of using `blocks` command:
```shell
go run cmd/ethcompare/main.go blocks --first-feed GatewayGRPC --second-feed Fiber --bloxroute-auth-header <bloxroute-auth-header> --fiber-auth-key <fiber-auth-key> --trail-time 10 --interval 180 --dump "ALL,MISSING" —first-feed-uri localhost:5002
```

### Latency compare
This benchmark is invoked by `feed-latency`, after compare is finish it generates output.csv file with hash, time gw sent tx and time we got it in feed and the diff command which has the following options:
```
 --first-feed-uri   first feed uri
 --auth-header      authorization header created with account id and password
 --interval         length of feed sample interval in seconds
```
The following command can be used to print help related to `feed-latency` command:
```shell
go run cmd/ethcompare/main.go feed-latency -h
```
#### Example
Here is an example of using `feed-latency` command:
```shell
go run cmd/ethcompare/main.go feed-latency --first-feed-uri <URI> --auth-header <YOUR HEADER> --interval  <INTERVAL>
```

## Installation
This package requires only Go to be installed in the system.
Dependencies should be downloaded automatically when `go run cmd/ethcompare/main.go`
is attempted for the first time.
