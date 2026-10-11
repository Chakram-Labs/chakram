#!/usr/bin/env bash
# scripts/deploy.sh — deploy a tagged Chakram release to testnet seed VMs.
#
# Usage:
#   ./scripts/deploy.sh v2.0.1              # deploy to all three VMs
#   ./scripts/deploy.sh v2.0.1 seed-2       # deploy to one VM (short or full name)
#
# Flow (runs entirely from your Mac — no internet access required from VMs):
#   1. Download chakram-linux-amd64 + SHA256SUMS from the GitHub release to /tmp
#   2. Verify SHA256 locally before touching any VM
#   3. For each VM: gcloud compute scp the binary, re-verify SHA256 on the VM,
#      atomic-install to /usr/local/bin/chakram, restart chakram-testnet.service
#
# Works for both private and public repos — the gh CLI handles auth on your Mac.
# The VMs never need to reach GitHub directly.
#
# Prerequisites:
#   - gh CLI authenticated (gh auth status)
#   - gcloud CLI authenticated with IAP-SSH access
#   - GCP_PROJECT set in the environment

set -euo pipefail

REPO="Chakram-Labs/chakram"
GCP_PROJECT="${GCP_PROJECT:-}"

[[ -z "$GCP_PROJECT" ]] && { echo "error: set GCP_PROJECT env var"; exit 1; }

TAG="${1:?usage: $0 <tag> [vm-name]}"
TARGET_VM="${2:-all}"

vm_zone() {
  case "$1" in
    chakram-seed-1) echo "asia-south1-c" ;;
    chakram-seed-2) echo "europe-west1-b" ;;
    chakram-seed-3) echo "us-central1-a" ;;
    *) echo "error: unknown VM '$1'" >&2; return 1 ;;
  esac
}

VMS=("chakram-seed-1" "chakram-seed-2" "chakram-seed-3")
if [[ "$TARGET_VM" != "all" ]]; then
  FULL_VM="chakram-${TARGET_VM#chakram-}"
  vm_zone "$FULL_VM" > /dev/null || { echo "error: unknown VM '$TARGET_VM'"; exit 1; }
  VMS=("$FULL_VM")
fi

# ── step 1: download release artifacts to Mac ─────────────────────────────────

STAGING="/tmp/chakram-deploy-${TAG}"
mkdir -p "$STAGING"
trap 'rm -rf "$STAGING"' EXIT

echo "==> downloading chakram-linux-amd64 and SHA256SUMS for $TAG"
gh release download "$TAG" \
  --repo "$REPO" \
  --pattern "chakram-linux-amd64" \
  --pattern "SHA256SUMS" \
  --dir "$STAGING" \
  --clobber

# ── step 2: verify SHA256 locally ─────────────────────────────────────────────

echo "==> verifying SHA256 locally"
(cd "$STAGING" && sha256sum --check --ignore-missing SHA256SUMS)
echo "    local checksum OK"

# Extract the expected SHA for use in remote verification.
EXPECTED_SHA=$(grep "chakram-linux-amd64" "$STAGING/SHA256SUMS" | awk '{print $1}')
chmod 0755 "$STAGING/chakram-linux-amd64"

# ── step 3: deploy to each VM ─────────────────────────────────────────────────

deploy_one() {
  local VM="$1"
  local ZONE
  ZONE="$(vm_zone "$VM")"

  echo ""
  echo "══════════════════════════════════════════════════════"
  echo "  Deploying $TAG → $VM  ($ZONE)"
  echo "══════════════════════════════════════════════════════"

  # 3a. copy binary to VM
  echo "[deploy] uploading chakram-linux-amd64 via IAP SCP"
  gcloud compute scp \
    "$STAGING/chakram-linux-amd64" \
    "$VM:/tmp/chakram-new" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap

  # 3b. verify on VM, install, restart
  gcloud compute ssh "$VM" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap \
    -- bash -s << EOF
  set -euo pipefail

  EXPECTED="$EXPECTED_SHA"

  # Re-verify SHA256 after transfer (guards against SCP corruption).
  ACTUAL=\$(sha256sum /tmp/chakram-new | awk '{print \$1}')
  if [[ "\$ACTUAL" != "\$EXPECTED" ]]; then
    echo "[deploy] CHECKSUM MISMATCH — aborting"
    echo "  expected: \$EXPECTED"
    echo "  got:      \$ACTUAL"
    rm -f /tmp/chakram-new
    exit 1
  fi
  echo "[deploy] remote checksum OK"

  # Atomic install: move into place as root.
  sudo install -o root -g root -m 0755 /tmp/chakram-new /usr/local/bin/chakram
  rm -f /tmp/chakram-new
  echo "[deploy] /usr/local/bin/chakram installed ($TAG)"

  # Restart consensus node; geth is not touched.
  sudo systemctl restart chakram-testnet.service
  echo "[deploy] chakram-testnet.service restarted"

  # Quick health check.
  sleep 3
  if sudo systemctl is-active --quiet chakram-testnet.service; then
    echo "[deploy] chakram-testnet.service is running ✓"
  else
    echo "[deploy] WARNING: service not running — last 30 log lines:"
    sudo journalctl -u chakram-testnet.service -n 30 --no-pager
    exit 1
  fi
EOF

  # Show the last few log lines from the Mac side for confirmation.
  echo ""
  echo "  Recent logs from $VM:"
  gcloud compute ssh "$VM" \
    --zone "$ZONE" \
    --project "$GCP_PROJECT" \
    --tunnel-through-iap \
    -- sudo journalctl -u chakram-testnet.service -n 15 --no-pager
}

for VM in "${VMS[@]}"; do
  deploy_one "$VM"
done

echo ""
echo "==> Deploy $TAG complete on: ${VMS[*]}"
