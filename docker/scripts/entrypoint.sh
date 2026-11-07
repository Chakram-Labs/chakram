#!/bin/sh
set -e

HOME_DIR="${CHAKRAM_HOME:-/data/chakram}"
CHAIN_ID="${CHAKRAM_CHAIN_ID:-chakram-devnet-1}"
REWARD_ADDR="${CHAKRAM_REWARD_ADDRESS:-}"
ENGINE_URL="${CHAKRAM_ENGINE_URL:-http://geth:8551}"
GETH_RPC="${CHAKRAM_GETH_RPC:-http://geth:8545}"
P2P_PORT="${CHAKRAM_P2P_PORT:-26656}"
RPC_PORT="${CHAKRAM_RPC_PORT:-26657}"
API_ADDR="${CHAKRAM_API_ADDR:-0.0.0.0:8080}"
PEERS="${CHAKRAM_PEERS:-}"
LOG_LEVEL="${CHAKRAM_LOG_LEVEL:-info}"
MODE="${CHAKRAM_MODE:-dev}"
API_DEV_MODE="${CHAKRAM_API_DEV_MODE:-false}"
FAUCET_ADDR="${CHAKRAM_FAUCET_ADDR:-}"
FAUCET_KEY="${CHAKRAM_FAUCET_KEY:-}"
P2P_HOST="${CHAKRAM_P2P_HOST:-}"

STAKING_CONTRACT="${CHAKRAM_STAKING_CONTRACT:-}"
LOG_FILE="$HOME_DIR/chakram.log"

mkdir -p "$HOME_DIR/config" "$HOME_DIR/data"

# If validator key is missing, this is a bare container (not pre-initialised by setup.sh).
# Run chakram init to generate fresh keys and a placeholder genesis.
if [ ! -f "$HOME_DIR/config/priv_validator_key.json" ]; then
  echo "[entrypoint] No validator key found — running chakram init..."
  chakram init --home "$HOME_DIR" --chain-id "$CHAIN_ID"
fi

# If genesis.json is missing (e.g. init generated a placeholder), check mounted file.
GENESIS_MOUNT="${CHAKRAM_GENESIS_FILE:-}"
if [ -n "$GENESIS_MOUNT" ] && [ -f "$GENESIS_MOUNT" ]; then
  echo "[entrypoint] Installing genesis from $GENESIS_MOUNT"
  cp "$GENESIS_MOUNT" "$HOME_DIR/config/genesis.json"
fi

if [ ! -f "$HOME_DIR/config/genesis.json" ]; then
  echo "[entrypoint] ERROR: genesis.json not found at $HOME_DIR/config/genesis.json"
  echo "  Run 'bash docker/setup.sh --mode $MODE' first."
  exit 1
fi

# Write config.toml if not present.
if [ ! -f "$HOME_DIR/config/config.toml" ]; then
  cat > "$HOME_DIR/config/config.toml" <<TOML
[p2p]
laddr = "tcp://0.0.0.0:${P2P_PORT}"
allow_duplicate_ip = true

[rpc]
laddr = "tcp://0.0.0.0:${RPC_PORT}"

[consensus]
timeout_propose = "2s"
timeout_prevote = "1s"
timeout_precommit = "1s"
timeout_commit = "500ms"
create_empty_blocks = true

[mempool]
type = "nop"

[blocksync]
enable = true
TOML
fi

# JWT secret
JWT_PATH="${CHAKRAM_JWT_PATH:-/run/secrets/jwt}"
if [ ! -f "$JWT_PATH" ]; then
  JWT_PATH="$HOME_DIR/jwt.hex"
  if [ ! -f "$JWT_PATH" ]; then
    echo "[entrypoint] Generating JWT secret at $JWT_PATH"
    dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n' > "$JWT_PATH"
    chmod 600 "$JWT_PATH"
  fi
fi

API_ARGS=""
if [ -n "$API_ADDR" ]; then
  API_ARGS="--api-addr $API_ADDR --api-log-file $LOG_FILE --api-static-dir /app/web"
  if [ "$API_DEV_MODE" = "true" ]; then
    API_ARGS="$API_ARGS --api-dev-mode"
    if [ -n "$FAUCET_ADDR" ] && [ -n "$FAUCET_KEY" ]; then
      API_ARGS="$API_ARGS --api-faucet-addr $FAUCET_ADDR --api-faucet-key $FAUCET_KEY"
    fi
  fi
  if [ -n "$P2P_HOST" ]; then
    API_ARGS="$API_ARGS --api-p2p-host $P2P_HOST"
  fi
fi

REWARD_ARG=""
if [ -n "$REWARD_ADDR" ]; then
  REWARD_ARG="--reward-address $REWARD_ADDR"
fi

PEERS_ARG=""
if [ -n "$PEERS" ]; then
  PEERS_ARG="--peers $PEERS"
fi

STAKING_ARG=""
if [ -n "$STAKING_CONTRACT" ]; then
  STAKING_ARG="--staking-contract $STAKING_CONTRACT"
fi

echo "[entrypoint] Starting chakram (mode=$MODE chain=$CHAIN_ID height=$(cat $HOME_DIR/data/priv_validator_state.json 2>/dev/null | grep height | head -1 || echo unknown))"

# Tee logs to file AND to docker stdout (docker logs still works).
exec chakram start \
  --home "$HOME_DIR" \
  --engine-url "$ENGINE_URL" \
  --jwt-path "$JWT_PATH" \
  --geth-rpc "$GETH_RPC" \
  --p2p-port "$P2P_PORT" \
  --rpc-port "$RPC_PORT" \
  --log-level "$LOG_LEVEL" \
  $REWARD_ARG \
  $API_ARGS \
  $PEERS_ARG \
  $STAKING_ARG \
  2>&1 | tee -a "$LOG_FILE"
