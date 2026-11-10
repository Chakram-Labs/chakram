#!/usr/bin/env python3
"""Call ChakramLocker.credit() for a range of blocks.

Reads each block's withdrawals, identifies the locker deposit (second
withdrawal = 80 % share), and attributes it to the validator that proposed
the block by calling credit(beneficiary, amount) on ChakramLocker.

Run this periodically (e.g. daily) to keep the locker attributions current.

Usage:
  python3 contracts/credit_locker.py \
    --rpc      https://rpc.chakram.one \
    --key      0x<owner-private-key>  \
    --locker   0x<ChakramLocker-address> \
    --staking  0x<StakingRegistry-address> \
    --from     1 \
    --to       latest
"""

import argparse
import json
import sys

from web3 import Web3
from web3.middleware import SignAndSendRawMiddlewareBuilder

LOCKER_ABI = [
    {
        "name": "credit",
        "type": "function",
        "inputs": [
            {"name": "beneficiary", "type": "address"},
            {"name": "amount",      "type": "uint256"},
        ],
        "outputs": [],
        "stateMutability": "nonpayable",
    },
    {
        "name": "locked",
        "type": "function",
        "inputs": [{"name": "beneficiary", "type": "address"}],
        "outputs": [{"name": "", "type": "uint256"}],
        "stateMutability": "view",
    },
    {
        "name": "claimable",
        "type": "function",
        "inputs": [{"name": "beneficiary", "type": "address"}],
        "outputs": [{"name": "", "type": "uint256"}],
        "stateMutability": "view",
    },
]

STAKING_ABI = [
    {
        "name": "validators",
        "type": "function",
        "inputs": [{"name": "", "type": "address"}],
        "outputs": [
            {"name": "pubKey",      "type": "bytes32"},
            {"name": "rewardAddr",  "type": "address"},
            {"name": "stake",       "type": "uint256"},
            {"name": "active",      "type": "bool"},
            {"name": "jailed",      "type": "bool"},
            {"name": "whitelisted", "type": "bool"},
            {"name": "registered",  "type": "bool"},
            {"name": "commission",  "type": "uint16"},
            {"name": "registeredAt","type": "uint256"},
        ],
        "stateMutability": "view",
    },
]


def rpc_call(w3, method, params):
    return w3.provider.make_request(method, params)["result"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rpc",     default="https://rpc.chakram.one")
    ap.add_argument("--key",     required=True,  help="Owner private key (0x...)")
    ap.add_argument("--locker",  required=True,  help="ChakramLocker contract address")
    ap.add_argument("--staking", required=True,  help="StakingRegistry contract address")
    ap.add_argument("--from",    dest="from_block", type=int, default=1)
    ap.add_argument("--to",      dest="to_block",   default="latest")
    ap.add_argument("--dry-run", action="store_true", help="Print credits without submitting")
    args = ap.parse_args()

    w3 = Web3(Web3.HTTPProvider(args.rpc))
    if not w3.is_connected():
        print(f"ERROR: cannot connect to {args.rpc}", file=sys.stderr)
        sys.exit(1)

    account = w3.eth.account.from_key(args.key)
    if not args.dry_run:
        w3.middleware_onion.inject(
            SignAndSendRawMiddlewareBuilder.build(account), layer=0
        )
    w3.eth.default_account = account.address

    locker  = w3.eth.contract(address=Web3.to_checksum_address(args.locker),  abi=LOCKER_ABI)
    staking = w3.eth.contract(address=Web3.to_checksum_address(args.staking), abi=STAKING_ABI)

    to_block = w3.eth.block_number if args.to_block == "latest" else int(args.to_block)
    print(f"Processing blocks {args.from_block}..{to_block}")
    print(f"Owner     : {account.address}")
    print(f"Locker    : {args.locker}")
    print()

    # Build rewardAddr → validator EVM address map from staking contract events
    # (simplified: we use the miner field + staking lookup)

    total_credited = 0
    skipped        = 0

    for height in range(args.from_block, to_block + 1):
        block = rpc_call(w3, "eth_getBlockByNumber", [hex(height), False])
        if not block:
            continue

        withdrawals = block.get("withdrawals", [])
        if len(withdrawals) < 2:
            # Single-withdrawal block (before locker was enabled) — skip.
            skipped += 1
            continue

        # Second withdrawal is the 80 % locker share.
        locker_w = withdrawals[1]
        amount_gwei = int(locker_w["amount"], 16)
        amount_wei  = amount_gwei * 10**9

        # The miner field (= SuggestedFeeRecipient) is the validator's reward address.
        beneficiary = Web3.to_checksum_address(block["miner"])

        if args.dry_run:
            print(f"  block {height}: credit {beneficiary} {w3.from_wei(amount_wei, 'ether'):.6f} CHK")
        else:
            tx = locker.functions.credit(beneficiary, amount_wei).transact(
                {"from": account.address, "gas": 200_000}
            )
            receipt = w3.eth.wait_for_transaction_receipt(tx)
            print(f"  block {height}: credited {w3.from_wei(amount_wei, 'ether'):.6f} CHK "
                  f"→ {beneficiary}  (tx {tx.hex()[:12]}... gas {receipt['gasUsed']})")

        total_credited += amount_wei

    print()
    print(f"Done. Total credited: {w3.from_wei(total_credited, 'ether'):.4f} CHK  "
          f"({skipped} single-withdrawal blocks skipped)")


if __name__ == "__main__":
    main()
