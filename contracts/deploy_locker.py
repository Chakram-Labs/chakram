#!/usr/bin/env python3
"""Deploy ChakramLocker.sol to a running Chakram node.

Usage:
  python3 contracts/deploy_locker.py \
    --rpc  https://rpc.chakram.one \
    --key  0x<owner-private-key>  \
    --lock-duration 31536000       # 1 year in seconds (default)
"""

import argparse
import json
import os
import subprocess
import sys

from web3 import Web3
from web3.middleware import SignAndSendRawMiddlewareBuilder

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
SOL_FILE   = os.path.join(SCRIPT_DIR, "ChakramLocker.sol")


def compile_contract():
    result = subprocess.run(
        ["solc", "--combined-json", "bin,abi", "--optimize", SOL_FILE],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        print("solc error:", result.stderr, file=sys.stderr)
        sys.exit(1)
    data = json.loads(result.stdout)
    contract_key = next(k for k in data["contracts"] if k.endswith(":ChakramLocker"))
    entry    = data["contracts"][contract_key]
    bytecode = "0x" + entry["bin"]
    abi      = entry["abi"] if isinstance(entry["abi"], list) else json.loads(entry["abi"])
    return bytecode, abi


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rpc",           default="https://rpc.chakram.one")
    ap.add_argument("--key",           required=True,  help="Owner private key (0x...)")
    ap.add_argument("--vest-cliff",  type=int, default=31_536_000,
                    help="Cliff in seconds before vesting starts (default: 31536000 = 1 year)")
    ap.add_argument("--vest-period", type=int, default=63_072_000,
                    help="Linear vesting period in seconds after cliff (default: 63072000 = 2 years)")
    args = ap.parse_args()

    w3 = Web3(Web3.HTTPProvider(args.rpc))
    if not w3.is_connected():
        print(f"ERROR: cannot connect to {args.rpc}", file=sys.stderr)
        sys.exit(1)

    account = w3.eth.account.from_key(args.key)
    w3.middleware_onion.inject(
        SignAndSendRawMiddlewareBuilder.build(account), layer=0
    )
    w3.eth.default_account = account.address

    cliff_days  = args.vest_cliff  // 86400
    period_days = args.vest_period // 86400
    print(f"Chain ID   : {w3.eth.chain_id}")
    print(f"Deployer   : {account.address}")
    print(f"Balance    : {w3.from_wei(w3.eth.get_balance(account.address), 'ether'):.4f} CHK")
    print(f"Cliff      : {args.vest_cliff}s ({cliff_days}d) — nothing claimable before this")
    print(f"Vest period: {args.vest_period}s ({period_days}d) — linear release after cliff")
    print(f"Full unlock: after {cliff_days + period_days}d total")
    print()
    print("Compiling ChakramLocker.sol ...")
    bytecode, abi = compile_contract()

    print("Deploying ...")
    Contract = w3.eth.contract(abi=abi, bytecode=bytecode)
    tx_hash  = Contract.constructor(account.address, args.vest_cliff, args.vest_period).transact(
        {"from": account.address, "gas": 2_000_000}
    )
    receipt  = w3.eth.wait_for_transaction_receipt(tx_hash)
    addr     = receipt["contractAddress"]

    print(f"\nChakramLocker deployed at: {addr}")
    print(f"Tx hash    : {tx_hash.hex()}")
    print(f"Gas used   : {receipt['gasUsed']}")
    print()
    print("Add to your node's systemd ExecStart:")
    print(f"  --locker-contract {addr}")


if __name__ == "__main__":
    main()
