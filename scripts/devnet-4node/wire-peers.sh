#!/usr/bin/env bash
# Connect all 4 geth nodes to each other via admin_addPeer for txpool gossip.
set -uo pipefail

declare -a GETH_HTTP_PORTS=(8545 8645 8745 8845)

echo "Wiring geth devp2p peers..."

# Collect enodes
declare -a ENODES
for i in 0 1 2 3; do
  PORT="${GETH_HTTP_PORTS[$i]}"
  ENODE=$(curl -sf -X POST "http://127.0.0.1:$PORT" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' 2>/dev/null | \
    python3 -c "import sys,json; print(json.load(sys.stdin)['result']['enode'])" 2>/dev/null || echo "")
  # Replace 0.0.0.0 with 127.0.0.1
  ENODE=$(echo "$ENODE" | sed 's/0\.0\.0\.0/127.0.0.1/')
  ENODES[$i]="$ENODE"
  echo "  node$i enode: ${ENODE:0:60}..."
done

# Peer all pairs
for i in 0 1 2 3; do
  for j in 0 1 2 3; do
    [[ $i -eq $j ]] && continue
    [[ -z "${ENODES[$j]}" ]] && continue
    PORT="${GETH_HTTP_PORTS[$i]}"
    curl -sf -X POST "http://127.0.0.1:$PORT" \
      -H "Content-Type: application/json" \
      -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_addPeer\",\"params\":[\"${ENODES[$j]}\"],\"id\":1}" > /dev/null 2>&1 || true
  done
done

echo "Peers wired."
