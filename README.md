# Public benchmarking scripts

## Required prerequisites

- python 3.x
- bloxroute-gateway
  - `pip install requests distro`
  - `pip install bloxroute-gateway`
- Local Go-gateway see [here](https://docs.bloxroute.com/gateway/gateway-installation/go-gateway) for instructions
- Local node or RPC node (Infura, Alchemy, etc)

## Repo consists of 3 benchmarking scripts

[Compare tx streams](compare_tx_feeds_time_diff.py) - Will compare mempool stream of txs from gateway vs node

- Default configuration will run 1 time for 60 seconds
- Example startup as follows -

```
python3 compare_tx_feeds_time_diff.py \
--gateway ws://127.0.0.1:28333/ws \
--auth-header <AUTH-HEADER> \
--eth ws://127.0.0.1:8546
```

[Compare blocks streams](compare_block_feeds.py) - Will compare stream of blocks from gateway vs node

- Default configuration will run 1 time for 60 seconds
- Example startup as follows -

```
python3 compare_block_feeds.py \
--gateway ws://127.0.0.1:28333/ws \
--auth-header <AUTH-HEADER> \
--eth ws://127.0.0.1:8546
```

[Compare sending txs](compare_tx_speed.py) - This script will submit conflicting txs with the same nonce to node and gateway, so only one tx will land on chain. This can be used as an indicator of sending tx speed.

- Ensure gateway and node are running on same network (default is BSC)
- Sample startup command
  - Will send group of 10 transactions with 3 second delay

```
python3 compare_tx_speed.py \
--node-ws-endpoint <NODE-ENDPOINT> \
--blxr-endpoint ws://127.0.0.1:28333/ws \
--blxr-auth-header <AUTH-HEADER> \
--sender-private-key <PRIVATE-KEY> \
--chain-id 56 \
--gas-price 5 \
--delay 3 \
--num-tx-groups 10
```

## If any issues are encountered please do not hesitate to reach out for support in our [Discord](https://discord.gg/Maz2ted6)
