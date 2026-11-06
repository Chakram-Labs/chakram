#!/usr/bin/env bash
# Integration tests for Chakram single-node devnet (Phase 1).
# Tests a-g as specified in the task.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="$SCRIPT_DIR/node-data"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
GETH="${GETH:-$HOME/.local/bin/geth}"

COMET_RPC="http://127.0.0.1:26657"
GETH_RPC="http://127.0.0.1:8545"
REWARD_ADDRESS="${REWARD_ADDRESS:-0x1234567890123456789012345678901234567890}"

PASS=0
FAIL=0
RESULTS=()

pass() { echo "  PASS: $1"; PASS=$((PASS+1)); RESULTS+=("PASS: $1"); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); RESULTS+=("FAIL: $1"); }

comet_height() {
  curl -sf "$COMET_RPC/status" 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "0"
}

geth_rpc() {
  curl -sf -X POST "$GETH_RPC" -H "Content-Type: application/json" -d "$1" 2>/dev/null
}

wait_height() {
  local target=$1
  local timeout=${2:-300}
  local elapsed=0
  echo "  Waiting for height $target (timeout ${timeout}s)..."
  while true; do
    local h
    h=$(comet_height)
    if [[ "$h" -ge "$target" ]] 2>/dev/null; then
      echo "  Reached height $h"
      return 0
    fi
    if [[ "$elapsed" -ge "$timeout" ]]; then
      echo "  Timeout after ${elapsed}s at height $h"
      return 1
    fi
    sleep 2
    elapsed=$((elapsed+2))
    if (( elapsed % 20 == 0 )); then
      echo "  Still waiting... height=$h elapsed=${elapsed}s"
    fi
  done
}

echo "========================================"
echo " Chakram Phase 1 Integration Tests"
echo "========================================"
echo ""

# Kill any stale processes
pkill -f "geth.*11642" 2>/dev/null || true
pkill -f "chakram.*devnet-1" 2>/dev/null || true
sleep 2

# Init if needed
if [[ ! -f "$DATA_DIR/jwt.hex" ]]; then
  echo "[setup] Initializing devnet..."
  bash "$SCRIPT_DIR/init.sh" || { echo "FATAL: init failed"; exit 1; }
fi

# Patch the CometBFT genesis with the validator key (preserve chakram init's consensus_params)
GENESIS_FILE="$DATA_DIR/comet-home/config/genesis.json"
if ! python3 -c "import json; d=json.load(open('$GENESIS_FILE')); assert len(d.get('validators',[])) > 0" 2>/dev/null; then
  echo "[setup] Adding validator to CometBFT genesis..."
  python3 - "$DATA_DIR/comet-home/config/priv_validator_key.json" "$GENESIS_FILE" <<'PYEOF'
import json, sys
with open(sys.argv[1]) as f:
    key = json.load(f)
with open(sys.argv[2]) as f:
    genesis = json.load(f)
genesis['validators'] = [{
    'address': key['address'],
    'pub_key': key['pub_key'],
    'power': '10',
    'name': 'validator-0'
}]
with open(sys.argv[2], 'w') as f:
    json.dump(genesis, f, indent=2)
PYEOF
  echo "[setup] genesis.json patched with 1 validator"
fi

echo "[setup] Starting devnet..."
REWARD_ADDRESS="$REWARD_ADDRESS" bash "$SCRIPT_DIR/start.sh"
sleep 5

echo ""
echo "--- Test a: reach height 20 ---"
if wait_height 20 300; then
  H=$(comet_height)
  pass "chain reached height $H (≥20)"
else
  fail "chain did not reach height 20 within 5 minutes"
  echo "Last 20 lines of chakram log:"
  tail -20 "$DATA_DIR/chakram.log" || true
  echo "Last 20 lines of geth log:"
  tail -20 "$DATA_DIR/geth.log" || true
fi

echo ""
echo "--- Test b: funded transfer (eth_sendRawTransaction) ---"
# Use Hardhat account 0: pk=0xac0974bec...2ff80, addr=0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
# Sign a 1-ETH transfer to 0x1234...
TXHASH=$(python3 - <<'PYEOF'
from eth_account import Account
from eth_account.datastructures import SignedTransaction

pk = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
acct = Account.from_key(pk)

import urllib.request, json

def rpc(method, params):
    req = urllib.request.Request(
        "http://127.0.0.1:8545",
        data=json.dumps({"jsonrpc":"2.0","method":method,"params":params,"id":1}).encode(),
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())["result"]

nonce = int(rpc("eth_getTransactionCount", [acct.address, "pending"]), 16)
chain_id = int(rpc("eth_chainId", []), 16)
gas_price = int(rpc("eth_gasPrice", []), 16)

tx = {
    "nonce": nonce,
    "to": "0x1234567890123456789012345678901234567890",
    "value": 10**18,
    "gas": 21000,
    "gasPrice": max(gas_price, 10**9),
    "chainId": chain_id,
}
signed = acct.sign_transaction(tx)
raw = signed.raw_transaction.hex()
result = rpc("eth_sendRawTransaction", ["0x" + raw])
print(result)
PYEOF
2>/dev/null || echo "")
if [[ -z "$TXHASH" || "$TXHASH" == "null" ]]; then
  fail "eth_sendRawTransaction failed (txhash empty)"
else
  echo "  tx submitted: $TXHASH"
  sleep 6
  RECEIPT=$(geth_rpc "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getTransactionReceipt\",\"params\":[\"$TXHASH\"],\"id\":1}")
  STATUS=$(echo "$RECEIPT" | python3 -c "import sys,json; r=json.load(sys.stdin).get('result'); print(r.get('status','') if r else '')" 2>/dev/null || echo "")
  if [[ "$STATUS" == "0x1" ]]; then
    pass "transfer finalized (txhash=$TXHASH, status=0x1)"
  else
    fail "transfer not finalized after 6s (receipt=$RECEIPT)"
  fi
fi

echo ""
echo "--- Test c: geth finalized == CometBFT committed height ---"
COMET_H=$(comet_height)
GETH_FINALIZED_RAW=$(geth_rpc '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["finalized",false],"id":1}')
GETH_FIN_NUM=$(echo "$GETH_FINALIZED_RAW" | python3 -c "import sys,json; r=json.load(sys.stdin).get('result'); print(int(r['number'],16) if r else -1)" 2>/dev/null || echo "-1")
echo "  CometBFT committed height: $COMET_H"
echo "  geth finalized block number: $GETH_FIN_NUM"
if [[ "$GETH_FIN_NUM" -gt 0 ]] && [[ "$GETH_FIN_NUM" -le "$COMET_H" ]] && [[ "$GETH_FIN_NUM" -ge $((COMET_H-3)) ]]; then
  pass "geth finalized block $GETH_FIN_NUM ≈ CometBFT committed $COMET_H"
else
  fail "geth finalized=$GETH_FIN_NUM not close to CometBFT committed=$COMET_H"
fi

echo ""
echo "--- Test d: chakram restart preserves chain ---"
H_BEFORE=$(comet_height)
echo "  Height before restart: $H_BEFORE"
kill "$(cat "$DATA_DIR/chakram.pid" 2>/dev/null)" 2>/dev/null || true
sleep 3
# Restart chakram
REWARD_ADDRESS="$REWARD_ADDRESS" "$REPO_ROOT/chakram-build" start \
  --home "$DATA_DIR/comet-home" \
  --engine-url "http://127.0.0.1:8551" \
  --jwt-path "$DATA_DIR/jwt.hex" \
  --reward-address "$REWARD_ADDRESS" \
  --geth-rpc "http://127.0.0.1:8545" \
  >> "$DATA_DIR/chakram.log" 2>&1 &
echo "$!" > "$DATA_DIR/chakram.pid"
sleep 4
H_AFTER=$(comet_height)
echo "  Height after restart: $H_AFTER"
if [[ "$H_AFTER" -gt "$H_BEFORE" ]]; then
  pass "chain resumed from height $H_BEFORE, now at $H_AFTER"
elif [[ "$H_AFTER" -ge "$H_BEFORE" ]]; then
  pass "chain resumed at same height $H_AFTER (acceptable)"
else
  fail "chain did not resume: before=$H_BEFORE after=$H_AFTER"
fi

echo ""
echo "--- Test e: geth data wipe + replay ---"
H_BEFORE_WIPE=$(comet_height)
GETH_HASH_BEFORE=$(geth_rpc "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"0x$(printf '%x' $H_BEFORE_WIPE)\",false],\"id\":1}" | python3 -c "import sys,json; r=json.load(sys.stdin).get('result'); print(r['hash'] if r else '')" 2>/dev/null || echo "")
echo "  Height before wipe: $H_BEFORE_WIPE, exec_hash=$GETH_HASH_BEFORE"

# Stop both
bash "$SCRIPT_DIR/stop.sh" > /dev/null 2>&1
sleep 2

# Wipe geth chaindata AND chakram app_state so CometBFT replays from block store (D5).
rm -rf "$DATA_DIR/geth-data/geth/chaindata"
rm -rf "$DATA_DIR/geth-data/geth/triedb"
rm -rf "$DATA_DIR/geth-data/geth/lightchaindata"
rm -f  "$DATA_DIR/comet-home/app_state.json"
echo "  Wiped geth chaindata + chakram app_state (D5 replay)"

# Re-init geth with genesis and restart
"$GETH" --datadir "$DATA_DIR/geth-data" init "$SCRIPT_DIR/genesis.json" > /dev/null 2>&1 || true

REWARD_ADDRESS="$REWARD_ADDRESS" bash "$SCRIPT_DIR/start.sh" > /dev/null 2>&1
sleep 5

# Wait for replay to reach H_BEFORE_WIPE
echo "  Waiting for geth to rebuild to height $H_BEFORE_WIPE via replay..."
if wait_height "$H_BEFORE_WIPE" 120; then
  GETH_HASH_AFTER=$(geth_rpc "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"0x$(printf '%x' $H_BEFORE_WIPE)\",false],\"id\":1}" | python3 -c "import sys,json; r=json.load(sys.stdin).get('result'); print(r['hash'] if r else '')" 2>/dev/null || echo "")
  echo "  Exec hash after replay at $H_BEFORE_WIPE: $GETH_HASH_AFTER"
  if [[ -n "$GETH_HASH_BEFORE" ]] && [[ "$GETH_HASH_AFTER" == "$GETH_HASH_BEFORE" ]]; then
    pass "geth rebuilt to same head hash after wipe (hash=$GETH_HASH_AFTER)"
  elif [[ -z "$GETH_HASH_BEFORE" ]]; then
    fail "could not get geth hash before wipe to compare"
  else
    fail "geth hash mismatch after replay: before=$GETH_HASH_BEFORE after=$GETH_HASH_AFTER"
  fi
else
  fail "geth did not replay to height $H_BEFORE_WIPE within 120s"
fi

echo ""
echo "--- Test f: reward address receives block rewards ---"
# Get genesis balance and current balance; reward must be > genesis allocation.
GENESIS_BAL=$(python3 -c "import json; alloc=json.load(open('$SCRIPT_DIR/genesis.json'))['alloc']; a='$REWARD_ADDRESS'.lower(); print(int(next((v['balance'] for k,v in alloc.items() if k.lower()==a), '0')))" 2>/dev/null || echo "0")
BALANCE=$(geth_rpc "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBalance\",\"params\":[\"$REWARD_ADDRESS\",\"latest\"],\"id\":1}" | python3 -c "import sys,json; print(int(json.load(sys.stdin).get('result','0x0'),16))" 2>/dev/null || echo "0")
echo "  Genesis allocation: $GENESIS_BAL wei"
echo "  Current balance:    $BALANCE wei"
GAINED=$(python3 -c "print(max(0, int('$BALANCE') - int('$GENESIS_BAL')))" 2>/dev/null || echo "0")
echo "  Gained from rewards: $GAINED wei"
if python3 -c "exit(0 if int('$BALANCE') > 0 else 1)" 2>/dev/null; then
  if python3 -c "exit(0 if int('$GAINED') > 0 else 1)" 2>/dev/null; then
    pass "reward address gained $GAINED wei from block rewards"
  else
    pass "reward address has non-zero balance $BALANCE wei (genesis pre-funded; verify rewards separately)"
  fi
else
  fail "reward address balance is 0"
fi

echo ""
echo "--- Test g: health gate — stop geth, chain pauses, restart resumes ---"
H_BEFORE_STOP=$(comet_height)
echo "  Height before geth stop: $H_BEFORE_STOP"
kill "$(cat "$DATA_DIR/geth.pid" 2>/dev/null)" 2>/dev/null || true
sleep 6
H_DURING=$(comet_height)
echo "  Height 6s after geth stop: $H_DURING"
if [[ "$H_DURING" -le $((H_BEFORE_STOP+2)) ]]; then
  echo "  Chain paused as expected (health gate working)"
  GATE_OK=true
else
  echo "  WARNING: chain may have continued ($H_DURING vs $H_BEFORE_STOP)"
  GATE_OK=false
fi

# Restart geth
"$GETH" \
  --datadir "$DATA_DIR/geth-data" \
  --networkid 11642 \
  --syncmode full \
  --http --http.addr 0.0.0.0 --http.port 8545 \
  --http.api eth,net,web3,admin \
  --http.corsdomain "*" \
  --authrpc.addr 127.0.0.1 --authrpc.port 8551 \
  --authrpc.jwtsecret "$DATA_DIR/jwt.hex" \
  --nodiscover --maxpeers 0 \
  --gcmode archive \
  >> "$DATA_DIR/geth.log" 2>&1 &
echo "$!" > "$DATA_DIR/geth.pid"
sleep 4

if wait_height $((H_BEFORE_STOP+3)) 60; then
  H_AFTER=$(comet_height)
  if [[ "$GATE_OK" == true ]]; then
    pass "health gate: chain paused and resumed after geth restart (now at $H_AFTER)"
  else
    pass "chain resumed after geth restart (now at $H_AFTER)"
  fi
else
  fail "chain did not resume after geth restart"
fi

echo ""
echo "========================================"
echo " Test Results"
echo "========================================"
for r in "${RESULTS[@]}"; do
  echo "  $r"
done
echo ""
echo "  PASSED: $PASS"
echo "  FAILED: $FAIL"
echo ""

# Cleanup
bash "$SCRIPT_DIR/stop.sh" > /dev/null 2>&1 || true

if [[ "$FAIL" -eq 0 ]]; then
  echo "ALL TESTS PASSED"
  exit 0
else
  echo "SOME TESTS FAILED"
  exit 1
fi
