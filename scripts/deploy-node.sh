#!/usr/bin/env bash
# Deploy code changes to the live Chakram node on the VM.
# Rsyncs source, rebuilds the Docker image, and restarts containers.
# Never touches docker/.env — VM secrets are always preserved.
#
# Run from repo root: ./scripts/deploy-node.sh [--no-rebuild]
#
# Options:
#   --no-rebuild   Skip Docker image rebuild (just restart containers)
set -euo pipefail

VM="chakram-node"
REMOTE="~/chakram3"
REBUILD=true

while [[ $# -gt 0 ]]; do
  case $1 in
    --no-rebuild) REBUILD=false; shift ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> Syncing code to VM (preserving docker/.env)..."
rsync -az --delete \
  --exclude='.git' \
  --exclude='docker/.env' \
  --exclude='docker/secrets/' \
  --exclude='node_modules' \
  --exclude='web/node_modules' \
  --exclude='web/dist' \
  --exclude='*.DS_Store' \
  --exclude='genesis-tool' \
  --exclude='genesis-tool-linux' \
  "$REPO_ROOT/" "${VM}:${REMOTE}/"

if $REBUILD; then
  echo "==> Rebuilding Docker image on VM..."
  ssh "$VM" "cd ${REMOTE} && docker build -f docker/Dockerfile -t chakram-chakram-dev . 2>&1 | tail -5"
fi

echo "==> Restarting containers..."
ssh "$VM" "cd ${REMOTE}/docker && docker compose --profile dev up -d 2>&1"

echo ""
echo "Done. Node updated at node.chakram.one"
