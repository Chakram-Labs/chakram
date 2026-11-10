#!/usr/bin/env bash
# Start all 4 Chakram devnet nodes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_ROOT="$SCRIPT_DIR/node-data"
GENESIS_DATA="$SCRIPT_DIR/genesis-data"
GETH="${GETH:-$HOME/.local/bin/geth}"
CHAKRAM="$REPO_ROOT/chakram-build"

# Port assignments (must match genesis-tool)
declare -a GETH_HTTP_PORTS=(8545 8645 8745 8845)
declare -a GETH_AUTH_PORTS=(8551 8651 8751 8851)
declare -a GETH_P2P_PORTS=(30303 30403 30503 30603)
declare -a COMET_P2P_PORTS=(26656 26756 26856 26956)
declare -a COMET_RPC_PORTS=(26657 26757 26857 26957)
declare -a EVM_ADDRS=(
  "0x1111111111111111111111111111111111111111"
  "0x2222222222222222222222222222222222222222"
  "0x3333333333333333333333333333333333333333"
  "0x4444444444444444444444444444444444444444"
)

if [[ ! -f "$DATA_ROOT/node0/jwt.hex" ]]; then
  echo "ERROR: not initialized. Run ./init-all.sh first."
  exit 1
fi

# Build persistent peer list for each node
declare -a PEER_IDS
for i in 0 1 2 3; do
  PEER_IDS[$i]=$(cat "$DATA_ROOT/node$i/node_id" 2>/dev/null || echo "")
done

build_peers() {
  local my_idx=$1
  local peers=""
  for j in 0 1 2 3; do
    if [[ $j != $my_idx ]]; then
      pid="${PEER_IDS[$j]}"
      port="${COMET_P2P_PORTS[$j]}"
      if [[ -n "$pid" ]]; then
        [[ -n "$peers" ]] && peers="$peers,"
        peers="${peers}${pid}@127.0.0.1:${port}"
      fi
    fi
  done
  echo "$peers"
}

echo "=== Starting Chakram 4-node devnet ==="

for i in 0 1 2 3; do
  NODE_DIR="$DATA_ROOT/node$i"
  GH="${GETH_HTTP_PORTS[$i]}"
  GA="${GETH_AUTH_PORTS[$i]}"
  GP="${GETH_P2P_PORTS[$i]}"
  CP="${COMET_P2P_PORTS[$i]}"
  CR="${COMET_RPC_PORTS[$i]}"
  EVM="${EVM_ADDRS[$i]}"
  PEERS=$(build_peers $i)

  echo "--- node$i ---"
  echo "  geth  http=:$GH auth=:$GA"
  echo "  comet p2p=:$CP rpc=:$CR"
  echo "  reward=$EVM"

  # Start geth
  "$GETH" \
    --datadir "$NODE_DIR/geth-data" \
    --networkid 11642 \
    --syncmode full \
    --http --http.addr 0.0.0.0 --http.port "$GH" \
    --http.api eth,net,web3,admin \
    --http.corsdomain "*" \
    --authrpc.addr 127.0.0.1 --authrpc.port "$GA" \
    --authrpc.jwtsecret "$NODE_DIR/jwt.hex" \
    --port "$GP" \
    --nodiscover --maxpeers 3 \
    --gcmode archive \
    > "$NODE_DIR/geth.log" 2>&1 &
  echo "$!" > "$NODE_DIR/geth.pid"
  echo "  geth started (PID $!)"

  # Wait for geth HTTP
  for k in $(seq 1 15); do
    if curl -sf -X POST "http://127.0.0.1:$GH" \
      -H "Content-Type: application/json" \
      -d '{"jsonrpc":"2.0","method":"net_version","params":[],"id":1}' > /dev/null 2>&1; then
      break
    fi
    sleep 1
  done

  # Start chakram
  "$CHAKRAM" start \
    --home "$NODE_DIR/comet-home" \
    --engine-url "http://127.0.0.1:$GA" \
    --jwt-path "$NODE_DIR/jwt.hex" \
    --reward-address "$EVM" \
    --geth-rpc "http://127.0.0.1:$GH" \
    --p2p-port "$CP" \
    --rpc-port "$CR" \
    --peers "$PEERS" \
    > "$NODE_DIR/chakram.log" 2>&1 &
  echo "$!" > "$NODE_DIR/chakram.pid"
  echo "  chakram started (PID $!)"
  sleep 1
done

echo ""
echo "All 4 nodes running."
echo "RPC endpoints:"
for i in 0 1 2 3; do
  echo "  node$i: geth=http://127.0.0.1:${GETH_HTTP_PORTS[$i]}  comet=http://127.0.0.1:${COMET_RPC_PORTS[$i]}"
done

echo ""
echo "=== Wiring geth devp2p peers ==="
bash "$SCRIPT_DIR/wire-peers.sh"

echo ""
echo "Test account: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (keystore imported, empty password)"
