#!/usr/bin/env bash
# Chakram node deploy script — works on any fresh Ubuntu 22.04/24.04 VM.
# Installs Docker + Go, builds chakramctl, inits the node, starts it.
#
# Usage (from repo root):
#   bash scripts/deploy.sh [OPTIONS]
#
# Options:
#   --mode          dev | fullnode        (default: dev)
#   --reward-addr   0xADDRESS            EVM address that receives block rewards
#   --peers         "id@ip:port,..."     CometBFT peers (required for fullnode mode)
#   --chain-id      chakram-devnet-1     Override chain ID
#   --rebuild                            Force Docker image rebuild
#   --help
#
# Examples:
#   # Seed / solo dev node
#   bash scripts/deploy.sh --mode dev --reward-addr 0xYOUR_ADDRESS
#
#   # Validator joining the live network
#   bash scripts/deploy.sh --mode fullnode --reward-addr 0xYOUR_ADDRESS \
#     --peers "abc123@34.14.218.57:26656"

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
MODE="dev"
REWARD_ADDR=""
PEERS=""
CHAIN_ID_ARG=""
REBUILD=false
GO_VERSION="1.27.0"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── Colour helpers ────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BOLD='\033[1m'; RESET='\033[0m'
info()    { echo -e "${GREEN}[chakram]${RESET} $*"; }
warn()    { echo -e "${YELLOW}[chakram]${RESET} $*"; }
err()     { echo -e "${RED}[chakram] ERROR:${RESET} $*" >&2; exit 1; }
section() { echo -e "\n${BOLD}── $* ──${RESET}"; }

# ── Args ──────────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --mode)         MODE="$2";        shift 2 ;;
    --reward-addr)  REWARD_ADDR="$2"; shift 2 ;;
    --peers)        PEERS="$2";       shift 2 ;;
    --chain-id)     CHAIN_ID_ARG="$2";shift 2 ;;
    --rebuild)      REBUILD=true;     shift   ;;
    --help|-h)
      sed -n '3,20p' "$0"; exit 0 ;;
    *) err "Unknown argument: $1" ;;
  esac
done

[[ "$MODE" != "dev" && "$MODE" != "fullnode" ]] && \
  err "--mode must be dev or fullnode"

[[ "$MODE" == "fullnode" && -z "$PEERS" ]] && \
  err "fullnode mode requires --peers (ask the network operator)"

# ── 1. Docker ────────────────────────────────────────────────────────────────
section "1/5  Docker"
if command -v docker &>/dev/null && docker compose version &>/dev/null; then
  info "Docker $(docker --version | awk '{print $3}' | tr -d ',') already installed"
else
  info "Installing Docker..."
  sudo apt-get update -qq
  sudo apt-get install -y -qq ca-certificates curl gnupg
  curl -fsSL https://get.docker.com | sudo sh
  sudo usermod -aG docker "$USER"
  info "Docker installed — you may need to log out and back in for group membership"
  info "Continuing with sudo for this session..."
  DOCKER_CMD="sudo docker"
fi
DOCKER_CMD="${DOCKER_CMD:-docker}"

# ── 2. Go ────────────────────────────────────────────────────────────────────
section "2/5  Go"
GOPATH_BIN="${GOPATH:-$HOME/go}/bin"
export PATH="$PATH:/usr/local/go/bin:$GOPATH_BIN"

if command -v go &>/dev/null; then
  info "Go $(go version | awk '{print $3}') already installed"
else
  info "Installing Go $GO_VERSION..."
  ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  GO_TAR="go${GO_VERSION}.linux-${ARCH}.tar.gz"
  curl -fsSL "https://dl.google.com/go/${GO_TAR}" -o /tmp/${GO_TAR}
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf /tmp/${GO_TAR}
  rm /tmp/${GO_TAR}
  # Persist to shell profile
  if ! grep -q '/usr/local/go/bin' ~/.bashrc 2>/dev/null; then
    echo 'export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin"' >> ~/.bashrc
  fi
  info "Go $GO_VERSION installed"
fi

# ── 3. Build chakramctl ───────────────────────────────────────────────────────
section "3/5  Build"
cd "$REPO_ROOT"
VERSION_FLAG="-X chakram/internal/version.Version=$(grep '^VERSION' Makefile | awk '{print $3}')"

info "Building chakramctl..."
go build -ldflags "$VERSION_FLAG" -o "$GOPATH_BIN/chakramctl" ./cmd/chakramctl/
info "chakramctl installed to $GOPATH_BIN/chakramctl"

info "Building genesis-tool..."
go build -ldflags "$VERSION_FLAG" -o "$REPO_ROOT/genesis-tool" ./cmd/genesis-tool/
info "genesis-tool built to $REPO_ROOT/genesis-tool"

# ── 4. Init ───────────────────────────────────────────────────────────────────
section "4/5  Init"
DOCKER_DIR="$REPO_ROOT/docker"

INIT_ARGS=(--mode "$MODE")
[[ -n "$CHAIN_ID_ARG" ]] && INIT_ARGS+=(--chain-id "$CHAIN_ID_ARG")

if [[ -f "$DOCKER_DIR/.env" ]]; then
  warn ".env already exists — skipping init (delete $DOCKER_DIR/.env to re-init)"
else
  info "Running chakramctl init..."
  "$GOPATH_BIN/chakramctl" init "${INIT_ARGS[@]}"
fi

# Patch .env with reward address and peers if provided
if [[ -n "$REWARD_ADDR" ]]; then
  sed -i "s|^CHAKRAM_REWARD_ADDRESS=.*|CHAKRAM_REWARD_ADDRESS=$REWARD_ADDR|" "$DOCKER_DIR/.env"
  info "Reward address set to $REWARD_ADDR"
fi

if [[ -n "$PEERS" ]]; then
  sed -i "s|^CHAKRAM_PEERS=.*|CHAKRAM_PEERS=$PEERS|" "$DOCKER_DIR/.env"
  info "Peers set"
fi

if [[ -n "$CHAIN_ID_ARG" ]]; then
  sed -i "s|^CHAKRAM_CHAIN_ID=.*|CHAKRAM_CHAIN_ID=$CHAIN_ID_ARG|" "$DOCKER_DIR/.env"
fi

# ── 5. Start ──────────────────────────────────────────────────────────────────
section "5/5  Start"
cd "$DOCKER_DIR"

COMPOSE_ARGS=(--profile "$MODE" up -d)
[[ "$REBUILD" == true ]] && COMPOSE_ARGS=(--profile "$MODE" up -d --build)

info "Starting Chakram node (mode=$MODE)..."
$DOCKER_CMD compose "${COMPOSE_ARGS[@]}"

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}Chakram node is running.${RESET}"
echo ""
echo -e "  Dashboard:  ${BOLD}http://$(curl -s ifconfig.me 2>/dev/null || echo '<VM_IP>'):8080${RESET}"
echo -e "  Geth RPC:   http://localhost:8545  (local only)"
echo -e "  P2P port:   26656 (CometBFT)  30303 (geth)"
echo ""
echo -e "  Logs:       ${BOLD}docker compose -f $DOCKER_DIR/docker-compose.yml --profile $MODE logs -f${RESET}"
echo -e "  Stop:       ${BOLD}docker compose -f $DOCKER_DIR/docker-compose.yml --profile $MODE down${RESET}"
echo ""
if [[ "$MODE" == "dev" ]]; then
  echo -e "  Next: open the Staking page, whitelist a validator address, and register."
fi
if [[ "$MODE" == "fullnode" ]]; then
  echo -e "  Next: share your validator address with the network operator to get whitelisted."
fi
