# Solana Streams Comparison Script

## Objective
Compare the stream speed difference between two endpoints (solana node endpoint, or bloXroute solana services endpoint).

## Usage
Startup arguments
```
--auth-header      bloXroute authorization header (default: "")
--endpoint-ws-uris comma separated list of two endpoints, the endpoint can be either solana node ws endpoint or bloXroute solana services endpoint (default: "")
--blxr-sub-req     subscribe request used for bloXroute solana services endpoint (default: '{"id": 1, "method": "subscribe", "params": ["orca", {"include": []}]}')
--interval         benchmark duration (default: 60)
--dump-all         dump receiving time data to a csv file (default: true)
```
The script sends `slotSubscribe` request to solana node endpoint, sends request specified by `--blxr-sub-req` argument 
to bloXroute solana services endpoint, and utilizes slot number in stream response to compare speed difference.

### Startup Command Example
- Compare solana node `wss://solana-api.projectserum.com` with solana node `wss://api.mainnet-beta.solana.com`:
```
go run main.go --endpoint-ws-uris node+wss://solana-api.projectserum.com,node+wss://api.mainnet-beta.solana.com
```

- Compare bloxroute solana service `wss://virginia.solana.blxrbdn.com/ws` with solana node `wss://api.mainnet-beta.solana.com`:
```
go run main.go --endpoint-ws-uris blxr+wss://virginia.solana.blxrbdn.com/ws,node+wss://api.mainnet-beta.solana.com --auth-header XXXXXX
```

- Compare raydium stream from bloxroute solana service `wss://virginia.solana.blxrbdn.com/ws` with raydium stream from bloxroute solana service `wss://frankfurt.solana.blxrbdn.com/ws`:
```
go run main.go --endpoint-ws-uris blxr+wss://virginia.solana.blxrbdn.com/ws,blxr+wss://frankfurt.solana.blxrbdn.com/ws --auth-header XXXXX --blxr-sub-req '{"id": 1, "method": "subscribe", "params": ["raydium", {"include": []}]}'
```

- Compare raydium stream from bloxroute solana service (DNS name) `wss://virginia.solana.blxrbdn.com/ws` with raydium stream from bloxroute solana service (IP address) `ws://52.29.119.212:28334/ws`:
```
go run main.go --endpoint-ws-uris blxr+wss://virginia.solana.blxrbdn.com/ws,blxr+ws://52.29.119.212:28334/ws --auth-header XXXXX --blxr-sub-req '{"id": 1, "method": "subscribe", "params": ["raydium", {"include": []}]}'
```

### Script Output Example
```
time="2022-08-01T10:18:50Z" level=info msg="The benchmark will run for 60 seconds"
time="2022-08-01T10:18:50Z" level=info msg="connecting to wss://solana-api.projectserum.com"
time="2022-08-01T10:18:50Z" level=info msg="connecting to wss://api.mainnet-beta.solana.com"
------------------------------------------------------------------
time="2022-08-01T10:18:51Z" level=info msg="End time: 2022-08-01 10:19:51.634542 -0500 CDT m=+60.946135843" 
time="2022-08-01T10:19:51Z" level=info msg="Streaming finished, processing..." 
time="2022-08-01T10:19:51Z" level=info msg="Dumping data to csv file BenchmarkOutput.csv"
Summary
Total number of slots seen: 94
Total slots from endpoint1[wss://solana-api.projectserum.com] : 91
Total slots from endpoint2[wss://api.mainnet-beta.solana.com] : 94
Total slots received from both: 91
Number of slots received first from endpoint1[wss://solana-api.projectserum.com] : 38
Number of slots received first from endpoint2[wss://api.mainnet-beta.solana.com] : 53
Percentage of slots seen first from endpoint2: 58.24
The average time difference for slots received first from endpoint1[wss://solana-api.projectserum.com] (ms): 591.32
The average time difference for slots received first from endpoint2[wss://api.mainnet-beta.solana.com] (ms): 703.49
On average, endpoint2 is faster than endpoint1 with a time difference (ms): 162.80
```
