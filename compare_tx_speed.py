import argparse
import asyncio
import datetime
import json
import requests
import sys
import time
from collections import defaultdict
from typing import Optional, Dict

from eth_account import Account
from eth_account.datastructures import SignedTransaction
from threading import Thread
from web3 import Web3

from bloxroute_cli.provider.ws_provider import WsProvider
from bxcommon.rpc.external.eth_ws_subscriber import EthWsSubscriber
from bxcommon.rpc.rpc_request_type import RpcRequestType


async def main() -> None:
    default_web3 = Web3(Web3.HTTPProvider())
    gas_limit = 22000
    parser = get_argument_parser()
    args = parser.parse_args()
    sender_private_key = args.sender_private_key
    blxr_endpoint = args.blxr_endpoint
    blxr_auth_header = args.blxr_auth_header
    num_tx_groups = args.num_tx_groups
    gas_price_wei = args.gas_price * int(1e9)
    chain_id = args.chain_id
    delay = args.delay
    gen_00tx_only = args.gen_00tx_only
    node_ws_endpoint = args.node_ws_endpoint

    # Check sender's wallet balance
    sender_account = Account.from_key(sender_private_key)  # pyre-ignore
    sender_address = sender_account.address

    node_ws = await EthWsSubscriber(node_ws_endpoint)
    gateway_ws = await WsProvider(uri=blxr_endpoint, headers={"Authorization": blxr_auth_header})

    nonce_result = await node_ws.call_rpc("eth_getTransactionCount", [sender_address, "latest"])
    nonce = int(nonce_result.result, 16)
    sender_balance_result = await node_ws.call_rpc("eth_getBalance", [sender_address, "latest"])
    sender_balance = int(sender_balance_result.result, 16)
    sender_balance_in_eth = default_web3.fromWei(
        sender_balance, "ether")  # pyre-ignore

    sender_expense = num_tx_groups * gas_price_wei * gas_limit
    if sender_balance < sender_expense:
        projected_ether = default_web3.fromWei(
            sender_expense, "ether")  # pyre-ignore
        print(f"Sender {sender_address} does not have enough balance for {num_tx_groups} groups of transactions. "
              f"Sender's balance is {sender_balance_in_eth} ETH, "
              f"while at least {projected_ether} ETH is required")
        sys.exit(0)

    print(f"Initial check completed. Sleeping {delay} sec.")
    time.sleep(delay)

    group_num_to_tx_hash = {}
    for i in range(1, num_tx_groups + 1):

        endpoint_to_tx_hash = {}
        print(f"Sending tx group {i}.")

        # bloXroute transaction
        tx = {
            "to": sender_address,
            "value": 0,
            "gas": gas_limit,
            "gasPrice": gas_price_wei,
            "nonce": nonce,
            "chainId": chain_id,
            "data": "0x11111111"
        }
        signed_blxr_tx = default_web3.eth.account.sign_transaction(
            tx, sender_private_key)

        endpoint_to_tx_hash[blxr_endpoint] = signed_blxr_tx.hash.hex()

        # ETH endpoint transactions
        endpoint = node_ws_endpoint

        tx = {
            "to": sender_address,
            "value": 0,
            "gas": gas_limit,
            "gasPrice": gas_price_wei,
            "nonce": nonce,
            "chainId": chain_id,
            "data": "0x22222222"
        }

        signed_node_tx = default_web3.eth.account.sign_transaction(
            tx, sender_private_key)

        endpoint_to_tx_hash[endpoint] = signed_node_tx.hash.hex()

        asyncio.create_task(send_tx_to_eth(
            node_ws, signed_node_tx.rawTransaction.hex()))
        asyncio.create_task(send_tx_to_blxr(
            gateway_ws, signed_blxr_tx.rawTransaction.hex()[2:]))
        await asyncio.sleep(5)

        nonce += 1
        group_num_to_tx_hash[i] = endpoint_to_tx_hash
        # Add a delay to all the groups except for the last group
        if i < num_tx_groups:
            print(f"Sleeping {delay} sec.")
            time.sleep(delay)

    print("Sleeping 1 min before checking transaction status.")
    time.sleep(60)

    endpoint_to_tx_mined = defaultdict(int)
    mined_tx_nums = set()
    sleep_left_minute = 4
    while len(mined_tx_nums) < num_tx_groups and sleep_left_minute > 0:
        for group_num in group_num_to_tx_hash:

            # Continue for confirmed transactions
            if group_num in mined_tx_nums:
                continue

            # Check transactions sent to different endpoints and find the confirmed one
            endpoint_to_tx_hash = group_num_to_tx_hash[group_num]
            for endpoint in endpoint_to_tx_hash:
                tx_hash = endpoint_to_tx_hash[endpoint]
                result = await node_ws.call_rpc("eth_getTransactionReceipt", [tx_hash])
                if result.result is not None:
                    endpoint_to_tx_mined[endpoint] += 1
                    mined_tx_nums.add(group_num)
                    break

        # When there is any pending transaction, maximum sleep time is 4 min
        if len(mined_tx_nums) < num_tx_groups:
            print(f"{num_tx_groups - len(mined_tx_nums)} transactions are pending. "
                  f"Sleeping 1 min before checking status again.")
            time.sleep(60)
            sleep_left_minute -= 1

    print("---------------------------------------------------------------------------------------------------------")
    print(f"Sent {num_tx_groups} groups of transactions to bloXroute and other ETH endpoints, "
          f"{len(mined_tx_nums)} of them have been confirmed: ")
    print(
        f"Number of confirmed transactions is {endpoint_to_tx_mined[node_ws_endpoint]} for ETH endpoint {node_ws_endpoint}.")
    print(f"Number of confirmed transactions is {endpoint_to_tx_mined[blxr_endpoint]} "
          f"for bloXroute endpoint {blxr_endpoint}.")


async def send_tx_to_blxr(ws: WsProvider, raw_tx: str):
    blxr_tx_response = await ws.call_bx(RpcRequestType.BLXR_TX, {"transaction": raw_tx})
    print(f"blxr response: {blxr_tx_response}")


async def send_tx_to_eth(ws: EthWsSubscriber, raw_tx: str):
    node_response = await ws.call_rpc("eth_sendRawTransaction", [raw_tx])
    print(f"node response: {node_response}")


def get_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument("--node-ws-endpoint", type=str, required=True,
                        help="Ethereum node ws endpoint. "
                             "Sample Input: ws://127.0.0.1:8546")
    parser.add_argument("--blxr-endpoint", type=str, default="wss://api.blxrbdn.com/ws",
                        help="bloXroute endpoint. Use wss://api.blxrbdn.com/ws for Cloud-API.")
    parser.add_argument("--blxr-auth-header", type=str,
                        help="bloXroute authorization header. Use base64 encoded value of "
                             "account_id:secret_hash for Cloud-API. For more information, see "
                             "https://bloxroute.com/docs/bloxroute-documentation/cloud-api/overview/")
    parser.add_argument("--sender-private-key", type=str, required=True,
                        help="Sender's private key, which starts with 0x.")
    parser.add_argument("--chain-id", type=int, default=1,
                        help="EVM chain id")
    parser.add_argument("--num-tx-groups", type=int, default=1,
                        help="Number of groups of transactions to submit.")
    parser.add_argument("--gas-price", type=int, required=True,
                        help="Transaction gas price in Gwei.")
    parser.add_argument("--delay", type=int, default=30,
                        help="Time (sec) to sleep between two consecutive groups.")
    parser.add_argument("--gen-00tx-only", action="store_true")
    return parser


if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    try:
        loop.run_until_complete(main())
    except KeyboardInterrupt:
        sys.exit(0)
