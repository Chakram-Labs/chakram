#!/usr/bin/env bash
# scripts/configure-peers.sh — configure geth devp2p static peers on all three seed nodes.
#
# Run this AFTER setup-vm.sh has completed on all three VMs and BEFORE starting
# any services. It reads each node's geth nodekey, builds the enode URLs, and
# writes static-nodes.json so geth instances can find each other for transaction
# propagation.
#
# Why this is necessary:
#   The Chakram consensus P2P (port 9101) only propagates blocks, not transactions.
#   Transactions submitted to seed-1's public RPC must reach seed-2's geth mempool
#   via geth's devp2p network, otherwise they will never be mined.
#
# Prerequisites:
#   - setup-vm.sh completed on chakram-seed-{1,2,3}
#   - GCP firewall rule allowing TCP 30303 inbound on all three VMs from each other:
#       gcloud compute firewall-rules create allow-geth-devp2p \
#         --project=chakram-foundation \
#         --direction=INGRESS \
#         --action=ALLOW \
#         --rules=tcp:30303 \
#         --source-ranges=34.14.218.57/32,207.175.24.63/32,136.80.159.167/32 \
#         --target-tags=chakram-seed
#   - GCP_PROJECT set in the environment

set -euo pipefail

GCP_PROJECT="${GCP_PROJECT:-}"
[[ -z "$GCP_PROJECT" ]] && { echo "error: set GCP_PROJECT env var"; exit 1; }

GETH_PORT=30303
NODEKEY="/var/lib/chakram/testnet/geth/geth/nodekey"
STATIC_NODES="/var/lib/chakram/testnet/geth/geth/static-nodes.json"

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

# ── step 1: collect enode public keys via bootnode -writeaddress ──────────────

echo "==> collecting geth node public keys"

get_pubkey() {
  local VM="$1" ZONE="$2"
  ssh_vm "$VM" "$ZONE" \
    sudo bootnode -nodekey "$NODEKEY" -writeaddress 2>/dev/null | tr -d '[:space:]'
}

PUB1=$(get_pubkey "$VM1" "$ZONE1")
PUB2=$(get_pubkey "$VM2" "$ZONE2")
PUB3=$(get_pubkey "$VM3" "$ZONE3")

[[ ${#PUB1} -eq 128 ]] || { echo "error: unexpected pubkey length for seed-1 (got ${#PUB1} chars)"; exit 1; }
[[ ${#PUB2} -eq 128 ]] || { echo "error: unexpected pubkey length for seed-2 (got ${#PUB2} chars)"; exit 1; }
[[ ${#PUB3} -eq 128 ]] || { echo "error: unexpected pubkey length for seed-3 (got ${#PUB3} chars)"; exit 1; }

ENODE1="enode://${PUB1}@${IP1}:${GETH_PORT}"
ENODE2="enode://${PUB2}@${IP2}:${GETH_PORT}"
ENODE3="enode://${PUB3}@${IP3}:${GETH_PORT}"

echo "  seed-1: $ENODE1"
echo "  seed-2: $ENODE2"
echo "  seed-3: $ENODE3"

# ── step 2: write static-nodes.json on each VM ────────────────────────────────

write_static_nodes() {
  local VM="$1" ZONE="$2" EA="$3" EB="$4"
  local TMP
  TMP=$(mktemp /tmp/static-nodes-XXXXXX.json)
  printf '[\n  "%s",\n  "%s"\n]\n' "$EA" "$EB" > "$TMP"
  scp_to_vm "$TMP" "$VM" "$ZONE" "/tmp/static-nodes.json"
  rm -f "$TMP"
  ssh_vm "$VM" "$ZONE" sudo bash -s << 'EOF'
    install -o chakram -g chakram -m 0640 \
      /tmp/static-nodes.json \
      /var/lib/chakram/testnet/geth/geth/static-nodes.json
    rm /tmp/static-nodes.json
EOF
  echo "  $VM: static-nodes.json written"
}

echo "==> writing static-nodes.json"
write_static_nodes "$VM1" "$ZONE1" "$ENODE2" "$ENODE3"
write_static_nodes "$VM2" "$ZONE2" "$ENODE1" "$ENODE3"
write_static_nodes "$VM3" "$ZONE3" "$ENODE1" "$ENODE2"

# ── step 3: also fix the geth unit on any VM set up with the old --maxpeers 0 ─
# (idempotent — setup-vm.sh would have written the correct unit on fresh VMs,
#  but seed-3 was set up before the fix)

echo "==> ensuring geth units have correct peer settings"

fix_geth_unit() {
  local VM="$1" ZONE="$2"
  ssh_vm "$VM" "$ZONE" sudo bash -s << 'EOF'
    set -euo pipefail
    UNIT=/etc/systemd/system/geth-testnet.service
    if grep -q "\-\-maxpeers 0" "$UNIT"; then
      sed -i 's/--maxpeers 0/--maxpeers 25/' "$UNIT"
      sed -i '/--maxpeers 25/a\  --port 30303 \\' "$UNIT"
      systemctl daemon-reload
      echo "  fixed --maxpeers on $(hostname)"
    else
      echo "  $(hostname): geth unit already correct"
    fi
EOF
}

fix_geth_unit "$VM1" "$ZONE1"
fix_geth_unit "$VM2" "$ZONE2"
fix_geth_unit "$VM3" "$ZONE3"

echo ""
echo "==> configure-peers done"
echo "    All three nodes will connect to each other on TCP $GETH_PORT"
echo ""
echo "    If you haven't already, open TCP $GETH_PORT in GCP firewall:"
echo "      gcloud compute firewall-rules create allow-geth-devp2p \\"
echo "        --project=$GCP_PROJECT \\"
echo "        --direction=INGRESS --action=ALLOW --rules=tcp:$GETH_PORT \\"
echo "        --source-ranges=${IP1}/32,${IP2}/32,${IP3}/32 \\"
echo "        --target-tags=chakram-seed"
echo ""
echo "    Next: ./scripts/deploy.sh <tag> — then start services"
