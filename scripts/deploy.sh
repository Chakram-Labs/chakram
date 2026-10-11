#!/usr/bin/env bash
# scripts/deploy.sh — deploy a tagged Chakram release to all testnet seed VMs.
#
# Usage:
#   ./scripts/deploy.sh v2.0.1              # deploy to all three VMs
#   ./scripts/deploy.sh v2.0.1 seed-2       # deploy to one VM only
#
# What it does on each VM (sequentially, not in parallel):
#   1. Downloads chakram-linux-amd64 from the GitHub release
#   2. Verifies the SHA256 against SHA256SUMS in the same release
#   3. Installs to /usr/local/bin/chakram (atomic rename)
#   4. Restarts chakram-testnet.service  (geth is NOT restarted)
#   5. Tails journald for 15 s so you can confirm a clean startup

set -euo pipefail

REPO="Chakram-Labs/chakram"
GCP_PROJECT="${GCP_PROJECT:-}"

[[ -z "$GCP_PROJECT" ]] && { echo "error: set GCP_PROJECT env var"; exit 1; }

TAG="${1:?usage: $0 <tag> [vm-name]}"
TARGET_VM="${2:-all}"

declare -A VM_ZONE=(
  [chakram-seed-1]="asia-south1-c"
  [chakram-seed-2]="europe-west1-b"
  [chakram-seed-3]="us-central1-a"
)

VMS=("chakram-seed-1" "chakram-seed-2" "chakram-seed-3")
if [[ "$TARGET_VM" != "all" ]]; then
  # Accept short form (seed-1) or full form (chakram-seed-1)
  FULL_VM="chakram-${TARGET_VM#chakram-}"
  [[ -z "${VM_ZONE[$FULL_VM]+_}" ]] && { echo "Unknown VM: $TARGET_VM"; exit 1; }
  VMS=("$FULL_VM")
fi

deploy_one() {
  local VM="$1"
  local ZONE="${VM_ZONE[$VM]}"

  echo ""
  echo "══════════════════════════════════════════════════════"
  echo "  Deploying $TAG → $VM  ($ZONE)"
  echo "══════════════════════════════════════════════════════"

  gcloud compute ssh "$VM" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap \
    -- bash -s << EOF
  set -euo pipefail

  TAG="$TAG"
  REPO="$REPO"

  # -- 1. download binary + checksum file ------------------------------------
  TMPDIR=\$(mktemp -d)
  trap 'rm -rf "\$TMPDIR"' EXIT

  echo "[deploy] downloading chakram-linux-amd64 from release \$TAG"
  curl -fsSL \
    "https://github.com/\$REPO/releases/download/\$TAG/chakram-linux-amd64" \
    -o "\$TMPDIR/chakram"

  echo "[deploy] downloading SHA256SUMS"
  curl -fsSL \
    "https://github.com/\$REPO/releases/download/\$TAG/SHA256SUMS" \
    -o "\$TMPDIR/SHA256SUMS"

  # -- 2. verify checksum ----------------------------------------------------
  cd "\$TMPDIR"
  # SHA256SUMS may contain multiple entries; grep for the linux binary.
  grep "chakram-linux-amd64" SHA256SUMS | sed 's/chakram-linux-amd64/chakram/' | sha256sum -c
  echo "[deploy] checksum OK"

  # -- 3. install atomically -------------------------------------------------
  chmod 0755 chakram
  sudo install -o root -g root -m 0755 chakram /usr/local/bin/chakram
  echo "[deploy] /usr/local/bin/chakram installed (version: \$(chakram --version 2>&1 || echo unknown))"

  # -- 4. restart consensus node (not geth) ----------------------------------
  sudo systemctl restart chakram-testnet.service
  echo "[deploy] chakram-testnet.service restarted"

  # -- 5. quick health check -------------------------------------------------
  sleep 3
  if sudo systemctl is-active --quiet chakram-testnet.service; then
    echo "[deploy] chakram-testnet.service is running ✓"
  else
    echo "[deploy] WARNING: service not running — showing last 30 log lines:"
    sudo journalctl -u chakram-testnet.service -n 30 --no-pager
    exit 1
  fi
EOF

  # Tail the logs from the Mac side for confirmation.
  echo ""
  echo "  Last 20 log lines from $VM:"
  gcloud compute ssh "$VM" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap \
    -- sudo journalctl -u chakram-testnet.service -n 20 --no-pager
}

for VM in "${VMS[@]}"; do
  deploy_one "$VM"
done

echo ""
echo "==> Deploy $TAG complete on: ${VMS[*]}"
