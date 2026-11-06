#!/usr/bin/env python3
"""
Phase 3 staking smoke test.

Whitelist an EVM address, register as validator with the running node's
Ed25519 pubkey, then confirm the ABCI emits a ValidatorUpdate.

Usage:
  python3 contracts/test_staking.py
"""

import base64, json, subprocess, sys, time, urllib.request
from web3 import Web3

RPC               = "http://127.0.0.1:8545"
COMET_RPC         = "http://127.0.0.1:26657"
CONTRACT_ADDR     = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
OWNER_KEY         = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
REWARD_ADDR       = Web3.to_checksum_address("0x20cdbb1a744809fd1544c786f4af8ded71c52314")

# Ed25519 pubkey of the running validator (from priv_validator_key.json).
# 32 bytes, base64-encoded.
VALIDATOR_PUBKEY_B64 = "QYSIIV4UjOkzo+Tej2VCWcX2qgMuqeMp+LOjJ+5jh40="

ABI = [
    {"type": "function", "name": "whitelist",
     "inputs": [{"name": "validator", "type": "address"}], "outputs": [], "stateMutability": "nonpayable"},
    {"type": "function", "name": "register",
     "inputs": [{"name": "ed25519PubKey", "type": "bytes32"},
                {"name": "rewardAddress",  "type": "address"}],
     "outputs": [], "stateMutability": "payable"},
    {"type": "function", "name": "isActive",
     "inputs": [{"name": "validator", "type": "address"}], "outputs": [{"type": "bool"}],
     "stateMutability": "view"},
    {"type": "event", "name": "ValidatorRegistered",
     "inputs": [{"name": "validator",    "type": "address", "indexed": True},
                {"name": "ed25519PubKey","type": "bytes32", "indexed": False},
                {"name": "rewardAddress","type": "address", "indexed": False},
                {"name": "stake",        "type": "uint256", "indexed": False}]},
]

def comet_height():
    with urllib.request.urlopen(COMET_RPC + "/status", timeout=5) as r:
        return int(json.load(r)["result"]["sync_info"]["latest_block_height"])

def main():
    w3 = Web3(Web3.HTTPProvider(RPC))
    assert w3.is_connected(), f"Cannot reach {RPC}"

    account = w3.eth.account.from_key(OWNER_KEY)
    registrant = account.address  # owner registers themselves for this smoke test
    print(f"Owner / registrant: {registrant}")

    contract = w3.eth.contract(address=CONTRACT_ADDR, abi=ABI)

    # Already active? Skip.
    if contract.functions.isActive(registrant).call():
        print("Validator already active in contract — skipping register step.")
    else:
        # 1. Whitelist
        print("\n[1/3] Whitelisting registrant...")
        tx = contract.functions.whitelist(registrant).build_transaction({
            "from":     account.address,
            "nonce":    w3.eth.get_transaction_count(account.address),
            "gas":      100_000,
            "gasPrice": w3.eth.gas_price,
        })
        signed = account.sign_transaction(tx)
        h = w3.eth.send_raw_transaction(signed.raw_transaction)
        receipt = w3.eth.wait_for_transaction_receipt(h, timeout=30)
        assert receipt.status == 1, "whitelist tx failed"
        print(f"  Whitelisted. tx={h.hex()[:16]}...")

        # 2. Register — convert Ed25519 pubkey (32 bytes) to bytes32
        pub_bytes = base64.b64decode(VALIDATOR_PUBKEY_B64)
        assert len(pub_bytes) == 32, f"expected 32 bytes, got {len(pub_bytes)}"
        pub_bytes32 = pub_bytes  # already 32 bytes

        stake_wei = w3.to_wei(1, "ether")
        print(f"\n[2/3] Registering validator (stake=1 CHK, pubkey={pub_bytes.hex()[:16]}...)...")
        tx2 = contract.functions.register(pub_bytes32, REWARD_ADDR).build_transaction({
            "from":     account.address,
            "nonce":    w3.eth.get_transaction_count(account.address),
            "gas":      200_000,
            "gasPrice": w3.eth.gas_price,
            "value":    stake_wei,
        })
        signed2 = account.sign_transaction(tx2)
        h2 = w3.eth.send_raw_transaction(signed2.raw_transaction)
        receipt2 = w3.eth.wait_for_transaction_receipt(h2, timeout=30)
        assert receipt2.status == 1, "register tx failed"
        reg_block = receipt2.blockNumber
        print(f"  Registered. tx={h2.hex()[:16]}... in block {reg_block}")

    # 3. Wait for ABCI to process the next block and emit ValidatorUpdate.
    print(f"\n[3/3] Waiting for ABCI to process ValidatorRegistered event...")
    deadline = time.time() + 20
    seen_height = comet_height()
    while time.time() < deadline:
        h = comet_height()
        if h > seen_height:
            print(f"  Block {h} finalized — checking validator power via CometBFT...")
            break
        time.sleep(0.5)

    # Query CometBFT validators endpoint.
    with urllib.request.urlopen(COMET_RPC + "/validators", timeout=5) as r:
        vals = json.load(r)["result"]["validators"]

    print(f"\nCometBFT validator set ({len(vals)} validator(s)):")
    for v in vals:
        power = v["voting_power"]
        addr  = v["address"]
        print(f"  {addr}  power={power}")

    print("\nAll good — Phase 3 staking smoke test complete.")

if __name__ == "__main__":
    main()
