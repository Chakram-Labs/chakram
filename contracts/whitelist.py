#!/usr/bin/env python3
"""Whitelist a validator address on the Chakram StakingRegistry.

Usage:
  python3 contracts/whitelist.py --address 0xVALIDATOR_ADDRESS [options]
  python3 contracts/whitelist.py --remove  0xVALIDATOR_ADDRESS [options]  # un-whitelist

Options:
  --address ADDR   EVM address to whitelist
  --remove  ADDR   EVM address to remove from whitelist
  --rpc     URL    Geth JSON-RPC endpoint (default: https://rpc.chakram.one)
  --contract ADDR  StakingRegistry address (read from docker/.env by default)
  --key     HEX    Owner private key (read from docker/.env by default)
"""

import argparse, json, os, sys
from web3 import Web3

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
ENV_PATH   = os.path.normpath(os.path.join(SCRIPT_DIR, "..", "docker", ".env"))

ABI = [
    {"type": "function", "name": "whitelist",
     "inputs": [{"name": "validator", "type": "address"}],
     "outputs": [], "stateMutability": "nonpayable"},
    {"type": "function", "name": "removeWhitelist",
     "inputs": [{"name": "validator", "type": "address"}],
     "outputs": [], "stateMutability": "nonpayable"},
    {"type": "function", "name": "validators",
     "inputs": [{"name": "", "type": "address"}],
     "outputs": [
         {"name": "ed25519PubKey",       "type": "bytes32"},
         {"name": "rewardAddress",       "type": "address"},
         {"name": "stake",               "type": "uint256"},
         {"name": "whitelisted",         "type": "bool"},
         {"name": "active",              "type": "bool"},
         {"name": "jailed",              "type": "bool"},
         {"name": "registered",          "type": "bool"},
         {"name": "commissionBps",       "type": "uint16"},
         {"name": "unbondingUnlockTime", "type": "uint256"},
     ], "stateMutability": "view"},
    {"type": "function", "name": "owner",
     "inputs": [], "outputs": [{"type": "address"}], "stateMutability": "view"},
]


def read_env(path):
    env = {}
    if not os.path.exists(path):
        return env
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, _, v = line.partition("=")
                env[k.strip()] = v.strip()
    return env


def main():
    ap = argparse.ArgumentParser(description="Whitelist a Chakram validator")
    ap.add_argument("--address",  help="EVM address to whitelist")
    ap.add_argument("--remove",   help="EVM address to remove from whitelist")
    ap.add_argument("--rpc",      default="https://rpc.chakram.one")
    ap.add_argument("--contract", default="")
    ap.add_argument("--key",      default="")
    args = ap.parse_args()

    if not args.address and not args.remove:
        ap.print_help()
        sys.exit(1)

    target_raw = args.address or args.remove
    removing   = bool(args.remove)

    # Load defaults from docker/.env
    env = read_env(ENV_PATH)
    owner_key    = args.key      or env.get("CHAKRAM_OWNER_KEY", "") or env.get("CHAKRAM_FAUCET_KEY", "")
    contract_addr = args.contract or env.get("CHAKRAM_STAKING_CONTRACT", "")

    if not owner_key:
        print("Error: owner private key not found.\n"
              "  Set CHAKRAM_OWNER_KEY in docker/.env or pass --key 0x...", file=sys.stderr)
        sys.exit(1)
    if not contract_addr:
        print("Error: staking contract address not found.\n"
              "  Set CHAKRAM_STAKING_CONTRACT in docker/.env or pass --contract 0x...", file=sys.stderr)
        sys.exit(1)

    w3 = Web3(Web3.HTTPProvider(args.rpc))
    if not w3.is_connected():
        print(f"Error: cannot connect to {args.rpc}", file=sys.stderr)
        sys.exit(1)

    try:
        target = Web3.to_checksum_address(target_raw)
    except Exception:
        print(f"Error: invalid address {target_raw!r}", file=sys.stderr)
        sys.exit(1)

    account  = w3.eth.account.from_key(owner_key)
    contract = w3.eth.contract(
        address=Web3.to_checksum_address(contract_addr),
        abi=ABI,
    )

    # Verify caller is owner
    on_chain_owner = contract.functions.owner().call()
    if on_chain_owner.lower() != account.address.lower():
        print(f"Error: signing key is {account.address} but contract owner is {on_chain_owner}", file=sys.stderr)
        sys.exit(1)

    # Show current state
    info = contract.functions.validators(target).call()
    print(f"Target address : {target}")
    print(f"Currently      : whitelisted={info[3]}  active={info[4]}  jailed={info[5]}")
    print()

    action = "removeWhitelist" if removing else "whitelist"
    fn     = contract.functions.removeWhitelist(target) if removing else contract.functions.whitelist(target)
    verb   = "Removing from whitelist" if removing else "Whitelisting"
    print(f"{verb}...")

    tx = fn.build_transaction({
        "from":     account.address,
        "nonce":    w3.eth.get_transaction_count(account.address),
        "gas":      100_000,
        "gasPrice": w3.eth.gas_price,
    })
    signed  = account.sign_transaction(tx)
    tx_hash = w3.eth.send_raw_transaction(signed.raw_transaction)
    print(f"Tx sent: {tx_hash.hex()}")

    receipt = w3.eth.wait_for_transaction_receipt(tx_hash, timeout=30)
    if receipt.status != 1:
        print("Transaction FAILED (status=0)", file=sys.stderr)
        sys.exit(1)

    print(f"Done. Block #{receipt.blockNumber}  gas used: {receipt.gasUsed:,}")

    # Confirm new state
    info2 = contract.functions.validators(target).call()
    print(f"\nNew state: whitelisted={info2[3]}  active={info2[4]}")


if __name__ == "__main__":
    main()
