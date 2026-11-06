#!/usr/bin/env bash
# Show status of all 4 devnet nodes.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_ROOT="$SCRIPT_DIR/node-data"

declare -a COMET_RPC_PORTS=(26657 26757 26857 26957)
declare -a GETH_HTTP_PORTS=(8545 8645 8745 8845)

echo "=== Chakram 4-node devnet status ==="
for i in 0 1 2 3; do
  echo ""
  echo "--- node$i ---"
  CR="${COMET_RPC_PORTS[$i]}"
  GH="${GETH_HTTP_PORTS[$i]}"

  # CometBFT height
  H=$(curl -sf "http://127.0.0.1:$CR/status" 2>/dev/null | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d['result']['sync_info']['latest_block_height'])" 2>/dev/null || echo "offline")
  echo "  comet height: $H"

  # Geth head
  GETH_H=$(curl -sf -X POST "http://127.0.0.1:$GH" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>/dev/null | \
    python3 -c "import sys,json; print(int(json.load(sys.stdin).get('result','0x0'),16))" 2>/dev/null || echo "offline")
  echo "  geth block:   $GETH_H"
done
echo ""
