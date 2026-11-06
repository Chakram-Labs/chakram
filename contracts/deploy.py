#!/usr/bin/env python3
"""Deploy StakingRegistry.sol to a running Chakram devnet.

Usage:
  python3 contracts/deploy.py [--rpc http://127.0.0.1:8545] [--min-stake 1] [--unbonding 60]
  python3 contracts/deploy.py --key 0xYOURKEY   # use own funded key instead of Hardhat #0
"""

import argparse, json, subprocess, sys, os
from web3 import Web3
from web3.middleware import SignAndSendRawMiddlewareBuilder

HARDHAT_KEY = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
SCRIPT_DIR  = os.path.dirname(os.path.abspath(__file__))
SOL_FILE    = os.path.join(SCRIPT_DIR, "StakingRegistry.sol")

def compile_contract():
    result = subprocess.run(
        ["solc", "--combined-json", "bin,abi", "--optimize", SOL_FILE],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        print("solc error:", result.stderr, file=sys.stderr)
        sys.exit(1)
    data = json.loads(result.stdout)
    contract_key = "contracts/StakingRegistry.sol:StakingRegistry"
    entry = data["contracts"][contract_key]
    bytecode = "0x" + entry["bin"]
    abi = entry["abi"] if isinstance(entry["abi"], list) else json.loads(entry["abi"])
    return bytecode, abi

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rpc",        default="http://127.0.0.1:8545")
    ap.add_argument("--key",        default=HARDHAT_KEY)
    ap.add_argument("--min-stake",  type=float, default=1.0,
                    help="Minimum stake in CHK (default: 1)")
    ap.add_argument("--unbonding",  type=int, default=60,
                    help="Unbonding period in seconds (default: 60 for devnet)")
    args = ap.parse_args()

    w3 = Web3(Web3.HTTPProvider(args.rpc))
    if not w3.is_connected():
        print(f"Cannot connect to {args.rpc}", file=sys.stderr)
        sys.exit(1)

    account = w3.eth.account.from_key(args.key)
    w3.middleware_onion.inject(
        SignAndSendRawMiddlewareBuilder.build(account), layer=0
    )
    w3.eth.default_account = account.address

    print(f"Connected to chain {w3.eth.chain_id}")
    print(f"Deployer: {account.address}")
    bal = w3.eth.get_balance(account.address)
    print(f"Balance:  {w3.from_wei(bal, 'ether'):.4f} CHK")

    min_stake_wei = w3.to_wei(args.min_stake, "ether")
    print(f"\nDeploying StakingRegistry(minStake={args.min_stake} CHK, unbonding={args.unbonding}s)...")

    bytecode, abi = compile_contract()
    contract = w3.eth.contract(abi=abi, bytecode=bytecode)

    tx_hash = contract.constructor(min_stake_wei, args.unbonding).transact(
        {"from": account.address, "gas": 5_000_000}
    )
    print(f"Tx sent: {tx_hash.hex()}")

    receipt = w3.eth.wait_for_transaction_receipt(tx_hash, timeout=60)
    if receipt.status != 1:
        print("Deploy FAILED (status=0)", file=sys.stderr)
        sys.exit(1)

    addr = receipt.contractAddress
    print(f"\nStakingRegistry deployed at: {addr}")
    print(f"Gas used: {receipt.gasUsed:,}")

    # Update docker/.env
    env_path = os.path.join(SCRIPT_DIR, "..", "docker", ".env")
    env_path = os.path.normpath(env_path)
    if os.path.exists(env_path):
        with open(env_path) as f:
            lines = f.readlines()
        updated = False
        for i, line in enumerate(lines):
            if line.startswith("CHAKRAM_STAKING_CONTRACT="):
                lines[i] = f"CHAKRAM_STAKING_CONTRACT={addr}\n"
                updated = True
                break
        if not updated:
            lines.append(f"CHAKRAM_STAKING_CONTRACT={addr}\n")
        with open(env_path, "w") as f:
            f.writelines(lines)
        print(f"\nUpdated docker/.env: CHAKRAM_STAKING_CONTRACT={addr}")
        print("Restart the devnet to activate staking:")
        print("  cd docker && docker compose --profile dev restart chakram-node-dev")
    else:
        print(f"\nSet in docker/.env:")
        print(f"  CHAKRAM_STAKING_CONTRACT={addr}")

if __name__ == "__main__":
    main()
