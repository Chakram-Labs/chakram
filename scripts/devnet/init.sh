#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_DIR="$SCRIPT_DIR/node-data"
GETH="${GETH:-$HOME/.local/bin/geth}"
CHAKRAM="$REPO_ROOT/chakram-build"

echo "=== Chakram single-node devnet init ==="

# Build chakram binary
echo "[1/5] Building chakram..."
cd "$REPO_ROOT"
go build -o chakram-build ./cmd/chakram/
cd "$SCRIPT_DIR"

mkdir -p "$DATA_DIR/geth-data"
mkdir -p "$DATA_DIR/comet-home"

# Init geth
echo "[2/5] Initializing geth datadir..."
"$GETH" --datadir "$DATA_DIR/geth-data" init "$SCRIPT_DIR/genesis.json" 2>&1 | grep -E "Successfully|ERROR" || true

# Generate JWT secret (geth requires 0x-prefixed hex; engine client strips it)
echo "[3/5] Generating JWT secret..."
printf "0x%s" "$(openssl rand -hex 32)" > "$DATA_DIR/jwt.hex"
echo "  JWT written to $DATA_DIR/jwt.hex"

# Init CometBFT home
echo "[4/5] Initializing CometBFT home..."
"$CHAKRAM" init \
  --home "$DATA_DIR/comet-home" \
  --chain-id "chakram-devnet-1" 2>&1

echo "[5/5] Done."
echo ""
echo "Data directory: $DATA_DIR"
echo "Validator key:  $DATA_DIR/comet-home/config/priv_validator_key.json"
echo ""
echo "IMPORTANT: Replace the genesis.json placeholder:"
echo "  cp <your-comet-genesis.json> $DATA_DIR/comet-home/config/genesis.json"
echo ""
echo "Then run: REWARD_ADDRESS=0xYourAddress ./start.sh"
