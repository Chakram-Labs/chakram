#!/usr/bin/env bash
# Initialize all 4 Chakram devnet nodes.
# Copies genesis-data into per-node data directories, initializes geth,
# and generates JWT secrets. Run once before start-all.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
GENESIS_DATA="$SCRIPT_DIR/genesis-data"
DATA_ROOT="$SCRIPT_DIR/node-data"
GETH="${GETH:-$HOME/.local/bin/geth}"

echo "=== Chakram 4-node devnet init ==="

# Build chakram binary
echo "[1/4] Building chakram..."
cd "$REPO_ROOT"
go build -o chakram-build ./cmd/chakram/
cd "$SCRIPT_DIR"

# Generate genesis-data if not present
if [[ ! -f "$GENESIS_DATA/comet-genesis.json" ]]; then
  echo "[2/4] Generating genesis data..."
  cd "$REPO_ROOT"
  go run ./cmd/genesis-tool/ --out "$GENESIS_DATA"
  cd "$SCRIPT_DIR"
else
  echo "[2/4] Using existing genesis-data"
fi

echo "[3/4] Initializing nodes..."
for i in 0 1 2 3; do
  NODE_DIR="$DATA_ROOT/node$i"
  echo "  node$i..."
  mkdir -p "$NODE_DIR/geth-data"
  mkdir -p "$NODE_DIR/comet-home/config"
  mkdir -p "$NODE_DIR/comet-home/data"

  # Init geth with shared geth genesis
  "$GETH" --datadir "$NODE_DIR/geth-data" init "$GENESIS_DATA/geth-genesis.json" > "$NODE_DIR/geth-init.log" 2>&1
  HASH=$(grep "Successfully" "$NODE_DIR/geth-init.log" | grep -o 'hash=[a-z0-9.]*' || echo "")
  echo "    geth init: $HASH"

  # Generate JWT secret (0x-prefixed for geth compat)
  printf "0x%s" "$(openssl rand -hex 32)" > "$NODE_DIR/jwt.hex"

  # Copy validator + node keys
  cp "$GENESIS_DATA/node$i/priv_validator_key.json" "$NODE_DIR/comet-home/config/"
  cp "$GENESIS_DATA/node$i/priv_validator_state.json" "$NODE_DIR/comet-home/data/"
  cp "$GENESIS_DATA/node$i/node_key.json" "$NODE_DIR/comet-home/config/"

  # Copy CometBFT genesis (shared)
  cp "$GENESIS_DATA/comet-genesis.json" "$NODE_DIR/comet-home/config/genesis.json"

  # Import devnet test account (Hardhat #0) into geth keystore with empty password
  # Private key: ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
  # Address:     0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
  TMPKEY=$(mktemp)
  echo "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80" > "$TMPKEY"
  TMPPASS=$(mktemp)
  echo "" > "$TMPPASS"
  "$GETH" account import --datadir "$NODE_DIR/geth-data" --password "$TMPPASS" "$TMPKEY" > /dev/null 2>&1 || true
  rm -f "$TMPKEY" "$TMPPASS"

  # Init CometBFT home via chakram init (writes config.toml, doesn't overwrite existing keys)
  "$REPO_ROOT/chakram-build" init \
    --home "$NODE_DIR/comet-home" \
    --chain-id "chakram-devnet-4" > /dev/null 2>&1 || true
  # Restore our generated keys (chakram init may have overwritten them)
  cp "$GENESIS_DATA/node$i/priv_validator_key.json" "$NODE_DIR/comet-home/config/"
  cp "$GENESIS_DATA/comet-genesis.json" "$NODE_DIR/comet-home/config/genesis.json"
done

# Write peer-id file for each node by reading node_key.json
echo "[4/4] Computing peer IDs..."
python3 - "$DATA_ROOT" "$GENESIS_DATA" <<'PYEOF'
import json, sys, os, base64, hashlib

data_root = sys.argv[1]
genesis_data = sys.argv[2]

for i in range(4):
    key_path = os.path.join(genesis_data, f"node{i}", "node_key.json")
    with open(key_path) as f:
        key = json.load(f)
    # CometBFT node ID = sha256(raw_pubkey)[:20] hex
    priv_b64 = key["priv_key"]["value"]
    raw = base64.b64decode(priv_b64)
    # Ed25519: first 32 bytes = private, last 32 bytes = public
    pub = raw[32:]
    # Node ID = hex of first 20 bytes of sha256(pubkey)
    node_id = hashlib.sha256(pub).hexdigest()[:40]
    with open(os.path.join(data_root, f"node{i}", "node_id"), "w") as f:
        f.write(node_id)
    print(f"  node{i} id: {node_id}")
PYEOF

echo ""
echo "Done. Start with: ./start-all.sh"
echo "EVM validator addresses:"
for i in 0 1 2 3; do
  echo "  node$i: $(python3 -c "import json; m=json.load(open('$GENESIS_DATA/validator-map.json')); print(m[$i]['evm_address'])")"
done
