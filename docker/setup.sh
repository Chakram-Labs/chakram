#!/usr/bin/env bash
# One-time Docker node setup for Chakram.
# Generates JWT secret, builds images, generates genesis, initialises geth.
#
# Usage:
#   cd docker/
#   bash setup.sh [--mode dev|localnet|fullnode] [--network-url URL] [--chain-id <id>] [--rebuild]
#
# Fullnode example:
#   bash setup.sh --mode fullnode --network-url https://node.chakram.one
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"

MODE="dev"
CHAIN_ID_ARG=""
REBUILD=false
NETWORK_URL=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --mode) MODE="$2"; shift 2 ;;
    --chain-id) CHAIN_ID_ARG="$2"; shift 2 ;;
    --rebuild) REBUILD=true; shift ;;
    --network-url) NETWORK_URL="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ "$MODE" != "dev" && "$MODE" != "localnet" && "$MODE" != "fullnode" ]]; then
  echo "ERROR: --mode must be dev, localnet, or fullnode"
  exit 1
fi

if [[ "$MODE" == "fullnode" && -z "$NETWORK_URL" ]]; then
  echo "ERROR: fullnode mode requires --network-url (e.g. https://node.chakram.one)"
  exit 1
fi

echo "=== Chakram Docker Setup (mode=$MODE) ==="

# ── 1. Create .env ────────────────────────────────────────────────────────────
if [[ ! -f "$SCRIPT_DIR/.env" ]]; then
  cp "$SCRIPT_DIR/.env.example" "$SCRIPT_DIR/.env"
  echo "[1/5] Created .env from .env.example"
else
  echo "[1/5] .env already exists"
fi

if [[ -n "$CHAIN_ID_ARG" ]]; then
  sed -i.bak "s/^CHAKRAM_CHAIN_ID=.*/CHAKRAM_CHAIN_ID=$CHAIN_ID_ARG/" "$SCRIPT_DIR/.env"
  rm -f "$SCRIPT_DIR/.env.bak"
fi

# Load env
set -a; source "$SCRIPT_DIR/.env"; set +a
CHAIN_ID="${CHAKRAM_CHAIN_ID:-chakram-devnet-1}"
NETWORK_ID="${CHAKRAM_NETWORK_ID:-11640}"

# Grafana: require a real password; reject the default placeholder
_GF_PW="${GRAFANA_ADMIN_PASSWORD:-}"
if [[ -z "$_GF_PW" || "$_GF_PW" == "chakram" ]]; then
  _GF_PW=$(openssl rand -hex 16)
  echo ""
  echo "*** Grafana admin password (save this): $_GF_PW"
  echo ""
  # Write to .env so docker compose picks it up
  if grep -q "^GRAFANA_ADMIN_PASSWORD=" "$SCRIPT_DIR/.env" 2>/dev/null; then
    sed -i.bak "s|^GRAFANA_ADMIN_PASSWORD=.*|GRAFANA_ADMIN_PASSWORD=$_GF_PW|" "$SCRIPT_DIR/.env"
    rm -f "$SCRIPT_DIR/.env.bak"
  else
    echo "GRAFANA_ADMIN_PASSWORD=$_GF_PW" >> "$SCRIPT_DIR/.env"
  fi
  export GRAFANA_ADMIN_PASSWORD="$_GF_PW"
fi

# Auto-detect and store external P2P host (used by /api/node-info)
_P2P_HOST="${CHAKRAM_P2P_HOST:-}"
if [[ -z "$_P2P_HOST" ]]; then
  _P2P_HOST=$(curl -sf --max-time 3 https://ifconfig.me 2>/dev/null || curl -sf --max-time 3 https://api.ipify.org 2>/dev/null || echo "")
  if [[ -n "$_P2P_HOST" ]]; then
    if grep -q "^CHAKRAM_P2P_HOST=" "$SCRIPT_DIR/.env" 2>/dev/null; then
      sed -i.bak "s|^CHAKRAM_P2P_HOST=.*|CHAKRAM_P2P_HOST=$_P2P_HOST|" "$SCRIPT_DIR/.env"
      rm -f "$SCRIPT_DIR/.env.bak"
    else
      echo "CHAKRAM_P2P_HOST=$_P2P_HOST" >> "$SCRIPT_DIR/.env"
    fi
    export CHAKRAM_P2P_HOST="$_P2P_HOST"
    echo "  P2P host: $_P2P_HOST"
  fi
fi

# ── 2. JWT secret ────────────────────────────────────────────────────────────
mkdir -p "$SCRIPT_DIR/secrets"
if [[ ! -f "$SCRIPT_DIR/secrets/jwt.hex" ]]; then
  dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n' \
    > "$SCRIPT_DIR/secrets/jwt.hex"
  chmod 600 "$SCRIPT_DIR/secrets/jwt.hex"
  echo "[2/5] Generated JWT secret at secrets/jwt.hex"
else
  echo "[2/5] JWT secret already exists"
fi

# ── 3. Build Go binaries + dashboard ─────────────────────────────────────────
echo "[3/5] Building binaries and dashboard..."

# Build genesis-tool
if [[ ! -f "$REPO_ROOT/genesis-tool" ]] || $REBUILD; then
  echo "  Building genesis-tool..."
  (cd "$REPO_ROOT" && go build -o genesis-tool ./cmd/genesis-tool/)
fi

# Build dashboard locally if npm is available (optional — Docker multi-stage build handles it)
if [[ ! -d "$REPO_ROOT/web/dist" ]] || $REBUILD; then
  if command -v npm &>/dev/null; then
    echo "  Building dashboard (npm)..."
    (cd "$REPO_ROOT/web" && npm install --prefer-offline --silent 2>/dev/null && npm run build 2>/dev/null) || true
  else
    echo "  Skipping dashboard pre-build (npm not found — Docker will build it)"
  fi
fi

# Build Docker images
BUILD_ARGS=""
if $REBUILD; then BUILD_ARGS="--no-cache"; fi
echo "  Building Docker images (2-3 min first time)..."
docker compose -f "$COMPOSE_FILE" --profile "$MODE" build $BUILD_ARGS
echo "  Docker images built."

# ── 4. Generate (or download) genesis files ───────────────────────────────────
echo "[4/5] Preparing genesis files..."

GENESIS_TMP="$(mktemp -d)"
trap 'rm -rf "$GENESIS_TMP"' EXIT

if [[ "$MODE" == "fullnode" ]]; then
  echo "  Downloading genesis from $NETWORK_URL ..."
  GENESIS_JSON=$(curl -sf "$NETWORK_URL/api/genesis") || {
    echo "ERROR: Cannot reach $NETWORK_URL/api/genesis"; exit 1
  }
  python3 - <<PYEOF
import json, sys, os
d = json.loads('''$GENESIS_JSON''')
with open('$GENESIS_TMP/comet-genesis.json', 'w') as f:
    json.dump(d['comet_genesis'], f, indent=2)
with open('$GENESIS_TMP/geth-genesis.json', 'w') as f:
    json.dump(d['geth_genesis'], f, indent=2)
PYEOF

  echo "  Fetching peer info ..."
  NODE_INFO=$(curl -sf "$NETWORK_URL/api/node-info") || {
    echo "ERROR: Cannot reach $NETWORK_URL/api/node-info"; exit 1
  }
  COMET_PEER=$(python3 -c "import json,sys; print(json.loads('''$NODE_INFO''')['comet_peer'])")
  GETH_ENODE=$(python3 -c "import json,sys; print(json.loads('''$NODE_INFO''')['geth_enode'])")

  # Auto-set chain ID from downloaded genesis
  FULLNODE_CHAIN_ID=$(python3 -c "import json; print(json.load(open('$GENESIS_TMP/comet-genesis.json'))['chain_id'])")
  if grep -q "^CHAKRAM_CHAIN_ID=" "$SCRIPT_DIR/.env" 2>/dev/null; then
    sed -i.bak "s|^CHAKRAM_CHAIN_ID=.*|CHAKRAM_CHAIN_ID=$FULLNODE_CHAIN_ID|" "$SCRIPT_DIR/.env"
    rm -f "$SCRIPT_DIR/.env.bak"
  else
    echo "CHAKRAM_CHAIN_ID=$FULLNODE_CHAIN_ID" >> "$SCRIPT_DIR/.env"
  fi

  # Write peers to .env
  if grep -q "^CHAKRAM_PEERS=" "$SCRIPT_DIR/.env" 2>/dev/null; then
    sed -i.bak "s|^CHAKRAM_PEERS=.*|CHAKRAM_PEERS=$COMET_PEER|" "$SCRIPT_DIR/.env"
    rm -f "$SCRIPT_DIR/.env.bak"
  else
    echo "CHAKRAM_PEERS=$COMET_PEER" >> "$SCRIPT_DIR/.env"
  fi
  if grep -q "^CHAKRAM_GETH_BOOTNODES=" "$SCRIPT_DIR/.env" 2>/dev/null; then
    sed -i.bak "s|^CHAKRAM_GETH_BOOTNODES=.*|CHAKRAM_GETH_BOOTNODES=$GETH_ENODE|" "$SCRIPT_DIR/.env"
    rm -f "$SCRIPT_DIR/.env.bak"
  else
    echo "CHAKRAM_GETH_BOOTNODES=$GETH_ENODE" >> "$SCRIPT_DIR/.env"
  fi

  chmod -R 755 "$GENESIS_TMP"
  echo "  Genesis downloaded (chain=$(python3 -c "import json; print(json.load(open('$GENESIS_TMP/comet-genesis.json'))['chain_id'])"))"
  echo "  CometBFT peer: $COMET_PEER"
  echo "  Geth enode:    $GETH_ENODE"
else
  VALIDATORS=4
  if [[ "$MODE" == "dev" ]]; then VALIDATORS=1; fi

  "$REPO_ROOT/genesis-tool" \
    --out "$GENESIS_TMP" \
    --chain-id "$CHAIN_ID" \
    --evm-chain-id "$NETWORK_ID" \
    --validators "$VALIDATORS"

  chmod -R 755 "$GENESIS_TMP"
  echo "  Genesis ready ($VALIDATORS validator(s), chain=$CHAIN_ID)"
fi

# ── 4b. Set reward address(es) unless already set by the user ────────────────
# genesis-tool generates a fresh EVM key per validator (node{i}/evm-key.json).
# Copy each key to secrets/ and write the address into .env unless
# CHAKRAM_REWARD_ADDRESS (dev) or CHAKRAM_NODE{n}_REWARD (localnet) is already
# set to a non-placeholder value.

_is_placeholder() {
  local val="$1"
  case "$val" in
    ""|0x1111*|0x2222*|0x3333*|0x4444*|0x0000*) return 0 ;;
    *) return 1 ;;
  esac
}

mkdir -p "$SCRIPT_DIR/secrets"

if [[ "$MODE" == "dev" ]]; then
  EVM_KEY_FILE="$GENESIS_TMP/node0/evm-key.json"
  if [[ -f "$EVM_KEY_FILE" ]]; then
    GEN_ADDR=$(python3 -c "import json,sys; print(json.load(open('$EVM_KEY_FILE'))['address'])" 2>/dev/null || echo "")
    GEN_PRIV=$(python3 -c "import json,sys; print(json.load(open('$EVM_KEY_FILE'))['private_key'])" 2>/dev/null || echo "")
    CURRENT_REWARD="${CHAKRAM_REWARD_ADDRESS:-}"
    if _is_placeholder "$CURRENT_REWARD" && [[ -n "$GEN_ADDR" ]]; then
      cp "$EVM_KEY_FILE" "$SCRIPT_DIR/secrets/evm-key-dev.json"
      chmod 600 "$SCRIPT_DIR/secrets/evm-key-dev.json"
      if grep -q "^CHAKRAM_REWARD_ADDRESS=" "$SCRIPT_DIR/.env" 2>/dev/null; then
        sed -i.bak "s|^CHAKRAM_REWARD_ADDRESS=.*|CHAKRAM_REWARD_ADDRESS=$GEN_ADDR|" "$SCRIPT_DIR/.env"
        rm -f "$SCRIPT_DIR/.env.bak"
      else
        echo "CHAKRAM_REWARD_ADDRESS=$GEN_ADDR" >> "$SCRIPT_DIR/.env"
      fi
      export CHAKRAM_REWARD_ADDRESS="$GEN_ADDR"
      # Also set faucet address + key so the faucet works out of the box
      if grep -q "^CHAKRAM_FAUCET_ADDR=" "$SCRIPT_DIR/.env" 2>/dev/null; then
        sed -i.bak "s|^CHAKRAM_FAUCET_ADDR=.*|CHAKRAM_FAUCET_ADDR=$GEN_ADDR|" "$SCRIPT_DIR/.env"
        rm -f "$SCRIPT_DIR/.env.bak"
      else
        echo "CHAKRAM_FAUCET_ADDR=$GEN_ADDR" >> "$SCRIPT_DIR/.env"
      fi
      if grep -q "^CHAKRAM_FAUCET_KEY=" "$SCRIPT_DIR/.env" 2>/dev/null; then
        sed -i.bak "s|^CHAKRAM_FAUCET_KEY=.*|CHAKRAM_FAUCET_KEY=$GEN_PRIV|" "$SCRIPT_DIR/.env"
        rm -f "$SCRIPT_DIR/.env.bak"
      else
        echo "CHAKRAM_FAUCET_KEY=$GEN_PRIV" >> "$SCRIPT_DIR/.env"
      fi
      echo "  Generated reward address: $GEN_ADDR (key: secrets/evm-key-dev.json)"
    else
      echo "  Using existing reward address: $CURRENT_REWARD"
    fi
  fi
elif [[ "$MODE" == "localnet" ]]; then
  for i in 0 1 2 3; do
    EVM_KEY_FILE="$GENESIS_TMP/node${i}/evm-key.json"
    if [[ -f "$EVM_KEY_FILE" ]]; then
      GEN_ADDR=$(python3 -c "import json,sys; print(json.load(open('$EVM_KEY_FILE'))['address'])" 2>/dev/null || echo "")
      VARNAME="CHAKRAM_NODE${i}_REWARD"
      CURRENT_VAL="${!VARNAME:-}"
      if _is_placeholder "$CURRENT_VAL" && [[ -n "$GEN_ADDR" ]]; then
        cp "$EVM_KEY_FILE" "$SCRIPT_DIR/secrets/evm-key-node${i}.json"
        chmod 600 "$SCRIPT_DIR/secrets/evm-key-node${i}.json"
        if grep -q "^${VARNAME}=" "$SCRIPT_DIR/.env" 2>/dev/null; then
          sed -i.bak "s|^${VARNAME}=.*|${VARNAME}=$GEN_ADDR|" "$SCRIPT_DIR/.env"
          rm -f "$SCRIPT_DIR/.env.bak"
        else
          echo "${VARNAME}=$GEN_ADDR" >> "$SCRIPT_DIR/.env"
        fi
        export "${VARNAME}"="$GEN_ADDR"
        echo "  node${i} reward address: $GEN_ADDR (key: secrets/evm-key-node${i}.json)"
      fi
    fi
  done
fi

GETH_IMAGE="ethereum/client-go:v1.17.6"

# ── 5. Initialise data volumes ────────────────────────────────────────────────
echo "[5/5] Initialising data volumes..."

init_node() {
  local idx=$1
  local suffix=$2          # "dev" | "node0" | "node1" | ...
  # Volume names use the explicit names declared in docker-compose.yml (name: chakram_*)
  local geth_vol="chakram_geth-${suffix}-data"
  local comet_vol="chakram_chakram-${suffix}-data"

  docker volume create "$geth_vol" > /dev/null 2>&1 || true
  docker volume create "$comet_vol" > /dev/null 2>&1 || true

  # Pre-chown the volume root so geth init runs as uid 1000 from the start.
  docker run --rm -v "$geth_vol:/data" alpine chown 1000:1000 /data
  # Init geth as uid 1000 — no post-init chown needed (idempotent).
  docker run --rm --user 1000:1000 \
    -v "$geth_vol:/data" \
    -v "$GENESIS_TMP:/genesis:ro" \
    "$GETH_IMAGE" \
    --datadir /data init /genesis/geth-genesis.json
  echo "  $suffix: geth data initialised"

  # Install CometBFT files (genesis + validator key + node key)
  # NOTE: volume is mounted at /home/chakram/.chakram in the container,
  # so write config/ and data/ directly at the volume root (not inside .chakram/).
  docker run --rm \
    -v "$comet_vol:/comet" \
    -v "$GENESIS_TMP:/genesis:ro" \
    alpine sh -c "
      mkdir -p /comet/config /comet/data
      cp /genesis/comet-genesis.json /comet/config/genesis.json
      cp /genesis/geth-genesis.json /comet/config/geth-genesis.json
      cp /genesis/node${idx}/priv_validator_key.json /comet/config/priv_validator_key.json
      cp /genesis/node${idx}/node_key.json /comet/config/node_key.json
      cp /genesis/node${idx}/config.toml /comet/config/config.toml
      printf '{\"height\":\"0\",\"round\":0,\"step\":0}' > /comet/data/priv_validator_state.json
      chmod 600 /comet/config/priv_validator_key.json
      # Copy EVM reward key so the node API can serve wallet info and sign staking txs.
      [ -f /genesis/node${idx}/evm-key.json ] && cp /genesis/node${idx}/evm-key.json /comet/reward-key.json && chmod 600 /comet/reward-key.json || true
      chown -R 1000:1000 /comet
    "
  echo "  $suffix: comet config installed"
}

init_fullnode() {
  local geth_vol="chakram_geth-fullnode-data"
  local comet_vol="chakram_chakram-fullnode-data"

  docker volume create "$geth_vol" > /dev/null 2>&1 || true
  docker volume create "$comet_vol" > /dev/null 2>&1 || true

  docker run --rm -v "$geth_vol:/data" alpine chown 1000:1000 /data
  docker run --rm --user 1000:1000 \
    -v "$geth_vol:/data" \
    -v "$GENESIS_TMP:/genesis:ro" \
    "$GETH_IMAGE" \
    --datadir /data init /genesis/geth-genesis.json
  echo "  fullnode: geth data initialised"

  # Install genesis only — keys are auto-generated by entrypoint.sh on first start.
  docker run --rm \
    -v "$comet_vol:/comet" \
    -v "$GENESIS_TMP:/genesis:ro" \
    alpine sh -c "
      mkdir -p /comet/config /comet/data
      cp /genesis/comet-genesis.json /comet/config/genesis.json
      cp /genesis/geth-genesis.json /comet/config/geth-genesis.json
      printf '{\"height\":\"0\",\"round\":0,\"step\":0}' > /comet/data/priv_validator_state.json
      chown -R 1000:1000 /comet
    "
  echo "  fullnode: comet config installed (keys auto-generated on first start)"
}

if [[ "$MODE" == "dev" ]]; then
  init_node 0 "dev"
elif [[ "$MODE" == "localnet" ]]; then
  for i in 0 1 2 3; do
    init_node "$i" "node$i"
  done
elif [[ "$MODE" == "fullnode" ]]; then
  init_fullnode
fi

echo ""
echo "=== Setup complete ==="
echo ""
echo "Start the node with:"
echo "  cd docker && docker compose --profile $MODE up -d"
echo ""
if [[ "$MODE" == "dev" ]]; then
  echo "  Dashboard:     http://127.0.0.1:8080"
  echo "  CometBFT RPC: http://127.0.0.1:26657"
  echo "  Geth RPC:     http://127.0.0.1:8545"
  echo "  Faucet:       POST http://127.0.0.1:8080/api/faucet  {\"address\":\"0x...\"}"
elif [[ "$MODE" == "localnet" ]]; then
  echo "  Dashboards:  :8080 :8081 :8082 :8083"
  echo "  CometBFT:    :26657 :26757 :26857 :26957"
fi
echo ""
if [[ "$MODE" == "dev" ]]; then
  OWNER_KEY=$(python3 -c "import json; print(json.load(open('$SCRIPT_DIR/secrets/evm-key-dev.json'))['private_key'])" 2>/dev/null || echo "")
  OWNER_ADDR=$(python3 -c "import json; print(json.load(open('$SCRIPT_DIR/secrets/evm-key-dev.json'))['address'])" 2>/dev/null || echo "")
  if [[ -n "$OWNER_KEY" ]]; then
    echo "Next: deploy the StakingRegistry contract once the node is running:"
    echo ""
    echo "  python3 $REPO_ROOT/contracts/deploy.py --rpc http://127.0.0.1:8545 --key $OWNER_KEY"
    echo ""
    echo "  Owner address: $OWNER_ADDR"
    echo "  (import this key into MetaMask to manage the contract)"
    echo ""
  fi
fi
echo "Other commands:"
echo "  docker compose --profile $MODE logs -f          # live logs"
echo "  docker compose --profile $MODE down             # stop"
echo "  docker compose --profile $MODE down -v          # stop + wipe data"
