#!/usr/bin/env bash
# scripts/setup-vm.sh — one-time VM initialisation for a Chakram 2.0 testnet seed node.
#
# Run from your Mac for each VM:
#   ./scripts/setup-vm.sh chakram-seed-1   # node + public RPC + Caddy
#   ./scripts/setup-vm.sh chakram-seed-2   # node + light miner
#   ./scripts/setup-vm.sh chakram-seed-3   # node only
#
# Prerequisites:
#   - gcloud CLI authenticated with an account that has IAP-secured SSH access
#   - GCP_PROJECT set in the environment or the variable below
#   - Firewall rule allowing TCP 9101 inbound on all three VMs
#   - DNS A record  rpc.testnet.chakram.one → 34.14.218.57  (before running seed-1)
#
# Idempotent: safe to re-run; skips steps that are already done.

set -euo pipefail

# ── config ────────────────────────────────────────────────────────────────────

GCP_PROJECT="${GCP_PROJECT:-}"          # override via env or fill in here
GETH_VERSION="v1.17.6"
GETH_SHA256="b358cd5abb0f5639cd2409877d73f3d5002d834c3e781734a1010dda1c0914ad"
GETH_COMMIT="3d84c6b2e3e9be032f8f2dcd9e900f89391fcdab"
# Go version required by geth v1.17.6 go.mod and our go.mod (whichever is higher).
# SHA256 sourced from https://go.dev/dl/?mode=json at the time of this commit.
GO_VERSION="1.25.0"
GO_SHA256="2852af0cb20a13139b3448992e69b868e50ed0f8a1e5940ee1de9e19a123b613"

# ── argument parsing ──────────────────────────────────────────────────────────

VM="${1:?usage: $0 <vm-name>}"

case "$VM" in
  chakram-seed-1)
    ZONE="asia-south1-c"
    PEERS="http://207.175.24.63:9101,http://136.80.159.167:9101"
    MINER_ADDR=""
    ;;
  chakram-seed-2)
    ZONE="europe-west1-b"
    PEERS="http://34.14.218.57:9101,http://136.80.159.167:9101"
    MINER_ADDR="0x211508Df9A6e9371D2DbfaFa5Dab348795E6DCeA"
    ;;
  chakram-seed-3)
    ZONE="us-central1-a"
    PEERS="http://34.14.218.57:9101,http://207.175.24.63:9101"
    MINER_ADDR=""
    ;;
  *)
    echo "error: unknown VM '$VM' (valid: chakram-seed-{1,2,3})"; exit 1 ;;
esac

IS_SEED1=false
IS_MINER=false
[[ "$VM" == "chakram-seed-1" ]] && IS_SEED1=true
[[ -n "$MINER_ADDR" ]] && IS_MINER=true

[[ -z "$GCP_PROJECT" ]] && { echo "error: set GCP_PROJECT env var"; exit 1; }

# ── helpers ───────────────────────────────────────────────────────────────────

remote() {
  gcloud compute ssh "$VM" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap \
    -- "$@"
}

# Run a multi-line heredoc as root on the remote machine.
remote_sudo() {
  remote sudo bash -s <<'ENDSUDO'
    set -euo pipefail
ENDSUDO
  # The above is just a guard; actual heredoc passed by caller via process substitution.
}

upload() {
  local src="$1" dst="$2"
  gcloud compute scp "$src" "$VM:$dst" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap
}

# ── step 0: OS check ─────────────────────────────────────────────────────────

echo "==> checking OS on $VM"
remote bash -c 'source /etc/os-release; echo "$PRETTY_NAME"'

# ── step 1: system packages ───────────────────────────────────────────────────

echo "==> installing system packages"
remote sudo bash -s << 'EOF'
  set -euo pipefail
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    git curl wget build-essential ca-certificates openssl golang-go
  # golang-go from apt may be older than required; will be replaced by go toolchain below
EOF

# ── step 2: Go toolchain (needed for geth source build) ──────────────────────

echo "==> ensuring Go $GO_VERSION"
remote bash -s << EOF
  set -euo pipefail
  if /usr/local/go/bin/go version 2>/dev/null | grep -q "go$GO_VERSION"; then
    echo "Go $GO_VERSION already installed"
    exit 0
  fi
  echo "Installing Go $GO_VERSION…"
  curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  echo "$GO_SHA256  /tmp/go.tar.gz" | sha256sum -c
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=\$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/golang.sh
  echo "Installed Go $GO_VERSION"
EOF

# ── step 3: chakram system user + data directory ─────────────────────────────

echo "==> creating chakram user and directories"
remote sudo bash -s << 'EOF'
  set -euo pipefail
  id chakram &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin chakram
  install -d -o chakram -g chakram -m 0750 /var/lib/chakram
  install -d -o chakram -g chakram -m 0750 /var/lib/chakram/testnet
  install -d -o chakram -g chakram -m 0750 /var/lib/chakram/testnet/geth
  install -d -o chakram -g chakram -m 0750 /var/lib/chakram/testnet/consensus
  if [[ ! -f /var/lib/chakram/testnet/jwt.hex ]]; then
    openssl rand -hex 32 > /var/lib/chakram/testnet/jwt.hex
    chown chakram:chakram /var/lib/chakram/testnet/jwt.hex
    chmod 0640 /var/lib/chakram/testnet/jwt.hex
    echo "JWT secret generated"
  else
    echo "JWT secret already exists"
  fi
EOF

# ── step 4: build geth from verified source ───────────────────────────────────

echo "==> building geth $GETH_VERSION (commit $GETH_COMMIT)"
remote bash -s << EOF
  set -euo pipefail
  export PATH="/usr/local/go/bin:\$PATH"

  if geth version 2>/dev/null | grep -q "$GETH_VERSION"; then
    echo "geth $GETH_VERSION already installed"
    exit 0
  fi

  echo "Downloading geth $GETH_VERSION source…"
  curl -fsSL -o /tmp/geth.tar.gz \
    "https://github.com/ethereum/go-ethereum/archive/refs/tags/$GETH_VERSION.tar.gz"

  echo "Verifying SHA256…"
  echo "$GETH_SHA256  /tmp/geth.tar.gz" | sha256sum -c
  echo "Checksum OK"

  tar -xzf /tmp/geth.tar.gz -C /tmp
  cd /tmp/go-ethereum-${GETH_VERSION#v}
  go build -o /tmp/geth ./cmd/geth
  sudo install -o root -g root -m 0755 /tmp/geth /usr/local/bin/geth
  rm -rf /tmp/geth /tmp/geth.tar.gz /tmp/go-ethereum-${GETH_VERSION#v}
  echo "geth installed: \$(geth version | head -1)"
EOF

# ── step 5: upload genesis file and initialise geth ──────────────────────────

echo "==> uploading testnet genesis and initialising geth"
upload config/genesis.testnet.json /tmp/genesis.testnet.json

remote bash -s << 'EOF'
  set -euo pipefail
  # Check if already initialised (genesis block exists in geth DB)
  if sudo -u chakram /usr/local/bin/geth \
       --datadir /var/lib/chakram/testnet/geth \
       dumpconfig 2>/dev/null | grep -q "chainId"; then
    echo "geth already initialised — skipping"
    exit 0
  fi
  sudo -u chakram /usr/local/bin/geth \
    init \
    --state.scheme hash \
    --datadir /var/lib/chakram/testnet/geth \
    /tmp/genesis.testnet.json
  echo "geth initialised from testnet genesis"
EOF

remote sudo rm -f /tmp/genesis.testnet.json

# ── step 6: install systemd units ─────────────────────────────────────────────

echo "==> installing systemd units"

remote sudo bash -s << 'EOF'
  set -euo pipefail
  cat > /etc/systemd/system/geth-testnet.service << 'UNIT'
[Unit]
Description=Geth execution client — Chakram testnet
Documentation=https://github.com/Chakram-Labs/chakram
After=network-online.target
Wants=network-online.target

[Service]
User=chakram
Group=chakram
ExecStart=/usr/local/bin/geth \
  --datadir /var/lib/chakram/testnet/geth \
  --syncmode full \
  --state.scheme hash \
  --gcmode archive \
  --cache 512 \
  --authrpc.addr 127.0.0.1 \
  --authrpc.port 8551 \
  --authrpc.jwtsecret /var/lib/chakram/testnet/jwt.hex \
  --http \
  --http.addr 127.0.0.1 \
  --http.port 8545 \
  --http.api eth,net,web3 \
  --nodiscover \
  --maxpeers 0
Restart=on-failure
RestartSec=10s
TimeoutStopSec=120s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
  echo "geth-testnet.service written"
EOF

# The chakram unit is node-specific (peers, miner flags).
CHAKRAM_EXTRA_FLAGS="--peers $PEERS --listen :9101"
if $IS_MINER; then
  CHAKRAM_EXTRA_FLAGS="$CHAKRAM_EXTRA_FLAGS --miner $MINER_ADDR --light"
else
  CHAKRAM_EXTRA_FLAGS="$CHAKRAM_EXTRA_FLAGS --no-mine"
fi

remote sudo bash -s << EOF
  set -euo pipefail
  cat > /etc/systemd/system/chakram-testnet.service << 'UNIT'
[Unit]
Description=Chakram consensus node — testnet
Documentation=https://github.com/Chakram-Labs/chakram
After=geth-testnet.service
Requires=geth-testnet.service

[Service]
User=chakram
Group=chakram
ExecStart=/usr/local/bin/chakram \
  --network testnet \
  --datadir /var/lib/chakram/testnet/consensus \
  --jwt /var/lib/chakram/testnet/jwt.hex \
  --authrpc http://127.0.0.1:8551 \
  --rpc http://127.0.0.1:8545 \
  $CHAKRAM_EXTRA_FLAGS
Restart=on-failure
RestartSec=10s
TimeoutStopSec=60s

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable geth-testnet.service chakram-testnet.service
  echo "systemd units installed and enabled"
EOF

# ── step 7: Caddy (seed-1 only) ───────────────────────────────────────────────

if $IS_SEED1; then
  echo "==> setting up Caddy on $VM (seed-1)"
  remote sudo bash -s << 'EOF'
    set -euo pipefail
    export PATH="/usr/local/go/bin:$PATH"

    if caddy version &>/dev/null; then
      echo "Caddy already installed: $(caddy version)"
    else
      # Install xcaddy to build Caddy with the rate-limit module.
      go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
      ~/go/bin/xcaddy build \
        --with github.com/mholt/caddy-ratelimit \
        --output /usr/local/bin/caddy
      echo "Caddy built: $(caddy version)"
    fi

    # Create Caddy user and directories if not present.
    id caddy &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin caddy
    install -d -o caddy -g caddy -m 0755 /etc/caddy
    install -d -o caddy -g caddy -m 0700 /var/lib/caddy
    install -d -o caddy -g caddy -m 0755 /var/log/caddy
EOF

  upload scripts/Caddyfile /tmp/Caddyfile
  remote sudo bash -s << 'EOF'
    set -euo pipefail
    install -o caddy -g caddy -m 0640 /tmp/Caddyfile /etc/caddy/Caddyfile
    rm /tmp/Caddyfile

    cat > /etc/systemd/system/caddy.service << 'UNIT'
[Unit]
Description=Caddy HTTPS reverse proxy — Chakram testnet RPC
After=network-online.target
Wants=network-online.target

[Service]
User=caddy
Group=caddy
ExecStart=/usr/local/bin/caddy run --config /etc/caddy/Caddyfile
ExecReload=/usr/local/bin/caddy reload --config /etc/caddy/Caddyfile
TimeoutStopSec=10s
Restart=on-failure
RestartSec=5s
ReadWritePaths=/var/lib/caddy /var/log/caddy

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable caddy.service
    echo "caddy.service installed and enabled"
EOF
fi

# ── done ─────────────────────────────────────────────────────────────────────

echo ""
echo "==> Setup complete for $VM"
echo "    Next: run  ./scripts/deploy.sh <tag>  to install the chakram binary"
echo "    Then: sudo systemctl start geth-testnet chakram-testnet"
if $IS_SEED1; then
  echo "    Then: sudo systemctl start caddy"
fi
