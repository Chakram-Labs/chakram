#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_DIR="$SCRIPT_DIR/node-data"
GETH="${GETH:-$HOME/.local/bin/geth}"
CHAKRAM="$REPO_ROOT/chakram-build"

REWARD_ADDRESS="${REWARD_ADDRESS:-0x1234567890123456789012345678901234567890}"

if [[ ! -f "$DATA_DIR/jwt.hex" ]]; then
  echo "ERROR: node-data not initialized. Run ./init.sh first."
  exit 1
fi

echo "=== Starting Chakram single-node devnet ==="
echo "Reward address: $REWARD_ADDRESS"

# Start geth in full sync mode — validators accept blocks via Engine API, not P2P snap sync.
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
  > "$DATA_DIR/geth.log" 2>&1 &
GETH_PID=$!
echo "geth started (PID $GETH_PID)"
echo "$GETH_PID" > "$DATA_DIR/geth.pid"

# Wait for geth authrpc to be ready
echo "Waiting for geth..."
for i in $(seq 1 20); do
  if curl -sf -X POST http://127.0.0.1:8545 \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"net_version","params":[],"id":1}' \
    > /dev/null 2>&1; then
    echo "geth ready (${i}s)"
    break
  fi
  sleep 1
done

# Start chakram
"$CHAKRAM" start \
  --home "$DATA_DIR/comet-home" \
  --engine-url "http://127.0.0.1:8551" \
  --jwt-path "$DATA_DIR/jwt.hex" \
  --reward-address "$REWARD_ADDRESS" \
  --geth-rpc "http://127.0.0.1:8545" \
  > "$DATA_DIR/chakram.log" 2>&1 &
CHAKRAM_PID=$!
echo "chakram started (PID $CHAKRAM_PID)"
echo "$CHAKRAM_PID" > "$DATA_DIR/chakram.pid"

echo ""
echo "Logs:"
echo "  geth:    $DATA_DIR/geth.log"
echo "  chakram: $DATA_DIR/chakram.log"
echo ""
echo "CometBFT RPC: http://127.0.0.1:26657"
echo "geth RPC:     http://127.0.0.1:8545"
