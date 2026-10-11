#!/usr/bin/env bash
# scripts/configure-peers.sh — configure geth devp2p static peers on all three seed nodes.
#
# Run this AFTER setup-vm.sh has completed on all three VMs and BEFORE starting
# services. It discovers each node's enode URL by starting geth briefly over IPC,
# then writes /etc/geth-testnet.toml with [Node.P2P] StaticNodes on each VM.
#
# Why this is necessary:
#   The Chakram consensus P2P (port 9101) propagates blocks only, not transactions.
#   Transactions submitted to seed-1's public RPC must reach seed-2's geth mempool
#   via geth's devp2p network, otherwise they will never be mined.
#
# TCP 30303 is already open via the allow-geth-devp2p firewall rule (chakram-node tag).
#
# Prerequisites:
#   - setup-vm.sh completed on all three VMs
#   - chakram binary NOT yet deployed / services NOT yet started
#   - GCP_PROJECT set in environment

set -euo pipefail

GCP_PROJECT="${GCP_PROJECT:-}"
[[ -z "$GCP_PROJECT" ]] && { echo "error: set GCP_PROJECT env var"; exit 1; }

GETH_PORT=30303
IPC="/var/lib/chakram/testnet/geth/geth/geth.ipc"
TOML="/etc/geth-testnet.toml"

VM1="chakram-seed-1"; ZONE1="asia-south1-c";   IP1="34.14.218.57"
VM2="chakram-seed-2"; ZONE2="europe-west1-b";  IP2="207.175.24.63"
VM3="chakram-seed-3"; ZONE3="us-central1-a";   IP3="136.80.159.167"

ssh_vm() {
  local VM="$1" ZONE="$2"
  shift 2
  gcloud compute ssh "$VM" \
    --zone "$ZONE" --project "$GCP_PROJECT" --tunnel-through-iap \
    -- "$@"
}

scp_to_vm() {
  local SRC="$1" VM="$2" ZONE="$3" DST="$4"
  gcloud compute scp "$SRC" "$VM:$DST" \
    --zone "$ZONE" --project "$GCP_PROJECT" --tunnel-through-iap
}

# ── step 1: collect enode URLs via a brief geth start + IPC attach ────────────
# cmd/bootnode was removed in go-ethereum v1.14+; cmd/devp2p exists but its
# 'key to-enode' subcommand flag syntax varies.  Starting geth briefly and
# reading admin.nodeInfo.enode over IPC is reliable for all versions.

get_enode() {
  local VM="$1" ZONE="$2" IP="$3"
  ssh_vm "$VM" "$ZONE" sudo bash << ENDSSH
    set -euo pipefail

    IPC="$IPC"
    DATADIR="/var/lib/chakram/testnet/geth"
    JWT="\$DATADIR/jwt.hex"
    rm -f "\$IPC"

    # Start geth using a scratch authrpc port (services not running yet) so
    # the IPC socket is created without competing with anything.
    sudo -u chakram /usr/local/bin/geth \\
      --datadir "\$DATADIR" \\
      --authrpc.addr 127.0.0.1 --authrpc.port 18551 \\
      --authrpc.jwtsecret "\$JWT" \\
      --nodiscover --maxpeers 0 --port 30398 \\
      2>/tmp/geth-probe.log &
    GPID=\$!

    # Wait up to 20 s for the IPC socket.
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
      [ -S "\$IPC" ] && break
      sleep 1
    done

    if [ ! -S "\$IPC" ]; then
      echo "[configure-peers] geth did not create IPC socket — last log:" >&2
      tail -20 /tmp/geth-probe.log >&2
      kill \$GPID 2>/dev/null; wait \$GPID 2>/dev/null
      exit 1
    fi

    ENODE=\$(/usr/local/bin/geth attach --exec "admin.nodeInfo.enode" "\$IPC" 2>/dev/null | tr -d '"')
    kill \$GPID 2>/dev/null; wait \$GPID 2>/dev/null
    rm -f /tmp/geth-probe.log "\$IPC"

    # Replace the IPC-reported IP (usually 0.0.0.0 or blank) with the real
    # external IP so the enode URL is routable from other VMs.
    echo "\$ENODE" | sed "s/@[^:]*:/@${IP}:/"
ENDSSH
}

echo "==> collecting enode URLs (starts geth briefly on each VM)"
ENODE1=$(get_enode "$VM1" "$ZONE1" "$IP1")
ENODE2=$(get_enode "$VM2" "$ZONE2" "$IP2")
ENODE3=$(get_enode "$VM3" "$ZONE3" "$IP3")

for PAIR in "seed-1:$ENODE1" "seed-2:$ENODE2" "seed-3:$ENODE3"; do
  NAME="${PAIR%%:*}"; URL="${PAIR#*:}"
  [[ "$URL" == enode://* ]] || { echo "error: bad enode for $NAME: $URL"; exit 1; }
  echo "  $NAME: $URL"
done

# ── step 2: write TOML static-nodes config on each VM ─────────────────────────
# geth reads [Node.P2P] StaticNodes from a TOML --config file regardless of
# which internal directory it resolves static-nodes.json to.

write_toml() {
  local VM="$1" ZONE="$2" EA="$3" EB="$4"
  local TMP
  TMP=$(mktemp /tmp/geth-testnet-XXXXXX.toml)
  cat > "$TMP" << EOF
[Node.P2P]
StaticNodes = [
  "$EA",
  "$EB"
]
EOF
  scp_to_vm "$TMP" "$VM" "$ZONE" "/tmp/geth-testnet.toml"
  rm -f "$TMP"

  ssh_vm "$VM" "$ZONE" sudo bash << 'ENDSSH'
    install -o chakram -g chakram -m 0640 /tmp/geth-testnet.toml /etc/geth-testnet.toml
    rm /tmp/geth-testnet.toml

    # Ensure the geth unit references --config (back-patching VMs set up with
    # an older version of setup-vm.sh that lacked this flag).
    UNIT=/etc/systemd/system/geth-testnet.service
    if ! grep -q "\-\-config" "$UNIT"; then
      # Insert --config line before the closing of ExecStart.
      sed -i 's/--port 30303$/--port 30303 \\\n  --config \/etc\/geth-testnet.toml/' "$UNIT"
      echo "  patched --config into geth unit on $(hostname)"
    fi
    systemctl daemon-reload
ENDSSH

  echo "  $VM: TOML written, geth unit OK"
}

echo ""
echo "==> writing static peer TOML on each VM"
write_toml "$VM1" "$ZONE1" "$ENODE2" "$ENODE3"
write_toml "$VM2" "$ZONE2" "$ENODE1" "$ENODE3"
write_toml "$VM3" "$ZONE3" "$ENODE1" "$ENODE2"

echo ""
echo "==> done — geth devp2p peers configured"
echo "    Peer graph: each node has static connections to the other two on TCP $GETH_PORT"
echo ""
echo "    Next steps:"
echo "      1. ./scripts/deploy.sh <tag>        — install the chakram binary"
echo "      2. Start services on all three VMs:"
echo "           gcloud compute ssh <vm> ... -- sudo systemctl start geth-testnet chakram-testnet"
echo "      3. ./scripts/verify-peers.sh        — confirm admin.peers shows 2 peers per node"
