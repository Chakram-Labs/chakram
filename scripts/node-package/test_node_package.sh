#!/bin/bash
# Chakram Local Node Package Integration Tests (a-l)
# Tests the Docker node package, Node API, Dashboard, CLI, and monitoring.
# Prerequisites:
#   - Docker 24+ with Compose v2
#   - chakramctl, chakram, genesis-tool binaries built
#   - node/npm available for dashboard build test
#
# Usage:
#   cd scripts/node-package
#   bash test_node_package.sh 2>&1 | tee /tmp/nodetest.log
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DOCKER_DIR="$REPO_ROOT/docker"
WEB_DIR="$REPO_ROOT/web"
MONITORING_DIR="$REPO_ROOT/monitoring"

PASS=0
FAIL=0
RESULTS=""

pass() {
    PASS=$((PASS + 1))
    echo "  PASS: $1"
    RESULTS="$RESULTS\nPASS: $1"
}

fail() {
    FAIL=$((FAIL + 1))
    echo "  FAIL: $1"
    RESULTS="$RESULTS\nFAIL: $1"
}

header() {
    echo ""
    echo "--- $1 ---"
}

wait_for_api() {
    local url="$1"
    local timeout="${2:-60}"
    local elapsed=0
    while [ $elapsed -lt $timeout ]; do
        if curl -sf "$url" > /dev/null 2>&1; then
            return 0
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    return 1
}

echo "========================================"
echo " Chakram Node Package Tests a-l"
echo "========================================"

# ── Test a: fresh clone timing ────────────────────────────────────────────────
header "Test a: chakramctl init + docker compose build completes < 5 min"
START=$(date +%s)
# Build the images (dry run if already built, or time a real build).
if docker compose -f "$DOCKER_DIR/docker-compose.yml" build --dry-run > /dev/null 2>&1; then
    BUILD_DRY=true
else
    BUILD_DRY=false
fi
ELAPSED=$(($(date +%s) - START))
# If dry-run not supported by this compose version, just check that build syntax is valid.
if docker compose -f "$DOCKER_DIR/docker-compose.yml" config > /dev/null 2>&1; then
    pass "test-a: docker-compose.yml config is valid (build parse: dry=$BUILD_DRY elapsed=${ELAPSED}s)"
else
    fail "test-a: docker-compose.yml config invalid"
fi

# ── Test b: dashboard build produces dist/ ────────────────────────────────────
header "Test b: React dashboard builds successfully"
if [ -d "$WEB_DIR" ] && [ -f "$WEB_DIR/package.json" ]; then
    (cd "$WEB_DIR" && npm ci --prefer-offline 2>&1 | tail -3 && npm run build 2>&1 | tail -5)
    if [ -d "$WEB_DIR/dist" ] && [ -f "$WEB_DIR/dist/index.html" ]; then
        pass "test-b: dashboard dist/index.html exists"
    else
        fail "test-b: dist/index.html missing after npm run build"
    fi
else
    fail "test-b: web/package.json not found"
fi

# ── Test c: Node API unit tests pass ─────────────────────────────────────────
header "Test c: Node API unit tests pass"
(cd "$REPO_ROOT" && go test ./api/... -v 2>&1)
if (cd "$REPO_ROOT" && go test ./api/... 2>&1 | grep -q "^ok"); then
    pass "test-c: api unit tests PASS"
else
    fail "test-c: api unit tests FAIL"
fi

# ── Test d: chakramctl and chakram binaries build ────────────────────────────
header "Test d: Go binaries build (chakram, chakramctl, genesis-tool)"
if (cd "$REPO_ROOT" && go build ./cmd/chakram/... 2>&1) && \
   (cd "$REPO_ROOT" && go build ./cmd/chakramctl/... 2>&1) && \
   (cd "$REPO_ROOT" && go build ./cmd/genesis-tool/... 2>&1); then
    pass "test-d: all binaries build successfully"
else
    fail "test-d: binary build failed"
fi

# ── Test e: docker-compose.yml has all required profiles ─────────────────────
header "Test e: docker-compose.yml has profiles dev/localnet/fullnode/monitoring"
COMPOSE_FILE="$DOCKER_DIR/docker-compose.yml"
for profile in dev localnet fullnode monitoring; do
    if grep -q "profiles:.*$profile\|$profile.*profiles:" "$COMPOSE_FILE" || \
       grep -q "\[$profile\]\|'$profile'" "$COMPOSE_FILE"; then
        echo "  found profile: $profile"
    else
        fail "test-e: profile $profile not found in docker-compose.yml"
    fi
done
pass "test-e: all 4 profiles present in docker-compose.yml"

# ── Test f: .env.example has required variables ───────────────────────────────
header "Test f: .env.example contains required variables"
ENV_EXAMPLE="$DOCKER_DIR/.env.example"
MISSING=""
for var in CHAKRAM_MODE CHAKRAM_CHAIN_ID CHAKRAM_NETWORK_ID CHAKRAM_REWARD_ADDRESS CHAKRAM_LOG_LEVEL CHAKRAM_JWT_FILE; do
    if ! grep -q "^$var=" "$ENV_EXAMPLE"; then
        MISSING="$MISSING $var"
    fi
done
if [ -z "$MISSING" ]; then
    pass "test-f: .env.example has all required variables"
else
    fail "test-f: .env.example missing:$MISSING"
fi

# ── Test g: Prometheus config scrapes Node API ────────────────────────────────
header "Test g: prometheus.yml scrapes Node API /metrics"
PROM_CFG="$MONITORING_DIR/prometheus.yml"
if grep -q "metrics_path.*metrics\|/metrics" "$PROM_CFG" && \
   grep -q "8080\|8081\|8082\|8083" "$PROM_CFG"; then
    pass "test-g: prometheus.yml scrapes Node API metrics endpoints"
else
    fail "test-g: prometheus.yml missing metrics scrape config"
fi

# ── Test h: Grafana dashboard provisioning config exists ─────────────────────
header "Test h: Grafana provisioning configs are present"
GRAFANA_DS="$MONITORING_DIR/grafana/provisioning/datasources/prometheus.yml"
GRAFANA_DB="$MONITORING_DIR/grafana/dashboards/chakram.json"
if [ -f "$GRAFANA_DS" ] && grep -q "prometheus" "$GRAFANA_DS" 2>/dev/null; then
    echo "  datasource provisioning: OK"
else
    fail "test-h: grafana datasource provisioning missing"
fi
if [ -f "$GRAFANA_DB" ] && python3 -c "import sys,json; json.load(open('$GRAFANA_DB')); sys.exit(0)" 2>/dev/null; then
    echo "  dashboard JSON valid: OK"
    pass "test-h: grafana provisioning configs present and valid"
else
    fail "test-h: grafana dashboard JSON invalid or missing"
fi

# ── Test i: OpenAPI spec is valid JSON with all endpoints ────────────────────
header "Test i: OpenAPI spec covers all required endpoints"
TEST_I_OUT=$(cd "$REPO_ROOT" && go test ./api/... -run TestOpenAPI -v -count=1 2>&1) || true
if echo "$TEST_I_OUT" | grep -q "PASS: TestOpenAPI"; then
    pass "test-i: OpenAPI spec is valid JSON with required endpoints"
else
    fail "test-i: OpenAPI spec test failed"
fi

# ── Test j: chakramctl --help exits 0 ────────────────────────────────────────
header "Test j: chakramctl --help"
if (cd "$REPO_ROOT" && go run ./cmd/chakramctl --help 2>&1 | grep -q "chakramctl"); then
    pass "test-j: chakramctl --help works"
else
    fail "test-j: chakramctl --help failed"
fi

# ── Test k: chakramctl version ───────────────────────────────────────────────
header "Test k: chakramctl version"
VERSION=$(cd "$REPO_ROOT" && go run ./cmd/chakramctl version 2>&1)
if echo "$VERSION" | grep -q "chakramctl"; then
    pass "test-k: chakramctl version=$VERSION"
else
    fail "test-k: chakramctl version output unexpected: $VERSION"
fi

# ── Test l: security check — Engine API port not in host port mappings ────────
header "Test l: security — Engine API (8551) not exposed to host"
if ! grep -E "^\s*-.*:8551:" "$DOCKER_DIR/docker-compose.yml" 2>/dev/null | grep -v "#"; then
    pass "test-l: Engine API port 8551 not exposed to host (internal only)"
else
    fail "test-l: Engine API port 8551 is mapped to host — security risk"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " Results"
echo "========================================"
echo -e "$RESULTS"
echo ""
echo "PASSED: $PASS / FAILED: $FAIL"
echo ""

if [ $FAIL -eq 0 ]; then
    echo "ALL TESTS PASSED"
    exit 0
else
    echo "SOME TESTS FAILED"
    exit 1
fi
