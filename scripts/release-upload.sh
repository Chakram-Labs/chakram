#!/usr/bin/env bash
# scripts/release-upload.sh
# Builds all chakram-node release binaries and uploads them to GCS.
# Run from anywhere — it always operates from the repo root.
# Requires: go, node/npm, gsutil (Google Cloud SDK)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BUCKET="chakram-bucket"
PUBLIC_BASE="https://storage.googleapis.com/${BUCKET}"
SA_KEY="${REPO_ROOT}/service-account.json"

# ── Auth ──────────────────────────────────────────────────────────────────────
if [[ ! -f "$SA_KEY" ]]; then
  echo "Error: service-account.json not found at ${SA_KEY}" >&2
  exit 1
fi
export GOOGLE_APPLICATION_CREDENTIALS="$SA_KEY"

# ── Version ───────────────────────────────────────────────────────────────────
VERSION=$(grep -oE '[0-9]+\.[0-9]+\.[0-9]+' internal/version/version.go | head -1)
if [[ -z "$VERSION" ]]; then
  echo "Error: could not read version from internal/version/version.go" >&2
  exit 1
fi
VERSION="v${VERSION}"

echo "========================================"
echo "  Chakram release upload: ${VERSION}"
echo "  Bucket : gs://${BUCKET}"
echo "========================================"
echo ""

# ── Web UI ────────────────────────────────────────────────────────────────────
echo "--> [1/5] Building web UI..."
cd web && npm ci --silent && npm run build && cd "$REPO_ROOT"
rm -rf cmd/chakram-node/webdist
cp -r web/dist cmd/chakram-node/webdist

# ── Binaries ──────────────────────────────────────────────────────────────────
echo "--> [2/5] Building binaries (all platforms)..."
mkdir -p dist
LDFLAGS="-X chakram/internal/version.Version=${VERSION}"

targets=(
  "linux   amd64 chakram-node-linux-amd64"
  "linux   arm64 chakram-node-linux-arm64"
  "darwin  amd64 chakram-node-darwin-amd64"
  "darwin  arm64 chakram-node-darwin-arm64"
  "windows amd64 chakram-node-windows-amd64.exe"
)

for t in "${targets[@]}"; do
  read -r os arch name <<< "$t"
  printf "    %-40s" "$name"
  GOOS=$os GOARCH=$arch go build -tags webembed -ldflags "$LDFLAGS" \
    -o "dist/${name}" ./cmd/chakram-node/
  echo "ok"
done

printf "    %-40s" "chakram-linux-amd64 (server)"
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" \
  -o dist/chakram-linux-amd64 ./cmd/chakram/
echo "ok"

# ── Checksums ─────────────────────────────────────────────────────────────────
echo "--> [3/5] Generating checksums..."
(cd dist && shasum -a 256 * > checksums.txt)
echo "    checksums.txt written"

# ── CORS (idempotent — safe to run every release) ─────────────────────────────
echo "--> [4/5] Uploading to gs://${BUCKET}/releases/${VERSION}/ ..."
CORS_JSON=$(mktemp /tmp/cors-XXXXXX.json)
cat > "$CORS_JSON" <<'CORS'
[{
  "origin": ["*"],
  "method": ["GET"],
  "responseHeader": ["Content-Type"],
  "maxAgeSeconds": 3600
}]
CORS
gsutil cors set "$CORS_JSON" "gs://${BUCKET}"
rm "$CORS_JSON"

# ── Upload versioned files ────────────────────────────────────────────────────
GCS_PATH="gs://${BUCKET}/releases/${VERSION}"
gsutil -m -h "Cache-Control:public,max-age=31536000,immutable" cp dist/* "${GCS_PATH}/"
echo "    Uploaded $(ls dist/ | wc -l | tr -d ' ') files"

# ── Upload latest.json ────────────────────────────────────────────────────────
echo "--> [5/5] Updating latest.json..."
cat > dist/latest.json <<JSON
{
  "version": "${VERSION}",
  "linux_amd64":   "${PUBLIC_BASE}/releases/${VERSION}/chakram-node-linux-amd64",
  "linux_arm64":   "${PUBLIC_BASE}/releases/${VERSION}/chakram-node-linux-arm64",
  "darwin_amd64":  "${PUBLIC_BASE}/releases/${VERSION}/chakram-node-darwin-amd64",
  "darwin_arm64":  "${PUBLIC_BASE}/releases/${VERSION}/chakram-node-darwin-arm64",
  "windows_amd64": "${PUBLIC_BASE}/releases/${VERSION}/chakram-node-windows-amd64.exe",
  "checksums":     "${PUBLIC_BASE}/releases/${VERSION}/checksums.txt"
}
JSON

gsutil -h "Cache-Control:no-cache,max-age=0" cp dist/latest.json "gs://${BUCKET}/releases/latest.json"
echo "    latest.json updated"

# ── Cleanup ───────────────────────────────────────────────────────────────────
rm -rf cmd/chakram-node/webdist

echo ""
echo "========================================"
echo "  Done!"
echo "  Files  : ${PUBLIC_BASE}/releases/${VERSION}/"
echo "  Latest : ${PUBLIC_BASE}/releases/latest.json"
echo "========================================"
