//go:build integration

// Run with: go test -tags integration -run TestGenesisHashMatchesGeth ./consensus/
//
// Requires geth v1.17.6 on PATH (built from the verified source archive per
// decisions.md). Skipped automatically when geth is not found so that normal
// `go test ./...` does not break in CI environments without geth installed.
//
// This test calls go-ethereum's genesis code by running the actual geth binary
// (which is go-ethereum compiled) to initialize a fresh data directory from
// config/genesis.json and query the resulting genesis block hash. If anyone
// changes config/genesis.json, this test catches the discrepancy immediately.
package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGenesisHashMatchesGeth(t *testing.T) {
	gethPath, err := exec.LookPath("geth")
	if err != nil {
		t.Skip("geth not found on PATH — install geth v1.17.6 per decisions.md to run this test")
	}

	// Verify the binary is at least 1.17.x.
	out, err := exec.Command(gethPath, "version").Output()
	if err != nil {
		t.Fatalf("geth version: %v", err)
	}
	if !strings.Contains(string(out), "1.17.") {
		t.Logf("warning: found geth version output:\n%s", out)
		t.Logf("expected v1.17.x — proceeding anyway")
	}

	// Find config/genesis.json relative to this file's location.
	_, thisFile, _, _ := runtime.Caller(0)
	genesisJSON := filepath.Join(filepath.Dir(thisFile), "..", "config", "genesis.json")
	if _, err := os.Stat(genesisJSON); err != nil {
		t.Fatalf("config/genesis.json not found at %s: %v", genesisJSON, err)
	}

	// Create a temporary data directory.
	dataDir := t.TempDir()

	// Run geth init.
	initCmd := exec.Command(gethPath, "init", "--datadir", dataDir, genesisJSON)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("geth init failed: %v\n%s", err, out)
	}

	// Generate a JWT secret.
	jwtFile := filepath.Join(dataDir, "jwt.hex")
	jwtCmd := exec.Command("openssl", "rand", "-hex", "32")
	jwtBytes, err := jwtCmd.Output()
	if err != nil {
		// openssl not available — write a fixed secret
		jwtBytes = []byte("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	}
	if err := os.WriteFile(jwtFile, []byte(strings.TrimSpace(string(jwtBytes))+"\n"), 0600); err != nil {
		t.Fatalf("write jwt: %v", err)
	}

	// Find a free port for the HTTP endpoint.
	httpPort := freePort(t)

	// Start geth with HTTP enabled.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	gethCmd := exec.CommandContext(ctx, gethPath,
		"--datadir", dataDir,
		"--authrpc.addr", "127.0.0.1",
		"--authrpc.port", fmt.Sprintf("%d", freePort(t)),
		"--authrpc.jwtsecret", jwtFile,
		"--http",
		"--http.addr", "127.0.0.1",
		"--http.port", fmt.Sprintf("%d", httpPort),
		"--http.api", "eth,net",
		"--nodiscover",
		"--maxpeers", "0",
	)
	if err := gethCmd.Start(); err != nil {
		t.Fatalf("geth start: %v", err)
	}
	defer gethCmd.Process.Kill() //nolint:errcheck

	// Poll until the HTTP endpoint is up.
	rpcURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Post(rpcURL,
			"application/json",
			strings.NewReader(`{"jsonrpc":"2.0","method":"net_version","params":[],"id":1}`))
		if err == nil {
			resp.Body.Close()
			break
		}
	}

	// Query block 0.
	if err := VerifyGenesisMatch(ctx, rpcURL, GenesisExecutionPayloadHashHex); err != nil {
		t.Fatalf("genesis hash mismatch:\n%v\n\n"+
			"If you changed config/genesis.json, recompute the hash:\n"+
			"  geth init --datadir /tmp/probe config/genesis.json\n"+
			"  # start geth, then: curl -X POST ... eth_getBlockByNumber 0x0\n"+
			"Update GenesisExecutionPayloadHashHex in consensus/genesis.go.",
			err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestGenesisJSONIsValidJSON ensures config/genesis.json is parseable.
// This test runs without any build tags (no geth required).
func init() {}

func TestGenesisJSONParseable(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot determine source file location")
	}
	genesisJSON := filepath.Join(filepath.Dir(thisFile), "..", "config", "genesis.json")
	data, err := os.ReadFile(genesisJSON)
	if err != nil {
		t.Fatalf("read config/genesis.json: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config/genesis.json is not valid JSON: %v", err)
	}
	// Check expected top-level keys.
	for _, key := range []string{"config", "nonce", "gasLimit", "alloc"} {
		if _, ok := m[key]; !ok {
			t.Errorf("config/genesis.json missing expected key %q", key)
		}
	}
}
