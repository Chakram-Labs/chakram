package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// gethCmd builds and starts the geth subprocess.
func startGeth(dataDir, jwtPath string) (*exec.Cmd, error) {
	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))

	args := []string{
		"--datadir", filepath.Join(dataDir, "geth-data"),
		"--networkid", "11640",
		// Geth is a pure execution engine — all block ordering and state
		// advancement comes from our consensus layer via the Engine API.
		// P2P discovery is disabled so geth cannot snap-sync state from peers
		// and race against the Engine API replay, which corrupts the trie.
		"--nodiscover",
		"--maxpeers", "0",
		"--syncmode", "full",
		"--gcmode", "full",
		"--history.transactions", "0",
		// Plain RPC (for chakram consensus + local API)
		"--http",
		"--http.addr", "127.0.0.1",
		"--http.port", "8545",
		"--http.api", "eth,net,web3,admin",
		"--http.vhosts", "localhost,127.0.0.1",
		// Engine API (for chakram consensus via JWT)
		"--authrpc.addr", "127.0.0.1",
		"--authrpc.port", "8551",
		"--authrpc.jwtsecret", jwtPath,
		"--authrpc.vhosts", "localhost,127.0.0.1",
	}

	cmd := exec.Command(gethBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start geth: %w", err)
	}
	return cmd, nil
}

// waitForGeth polls geth's RPC until it responds or the context is cancelled.
func waitForGeth(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	payload := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for geth")
		default:
		}
		resp, err := client.Post("http://127.0.0.1:8545", "application/json",
			strings.NewReader(payload))
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// gethBlockNumber returns the current block number from local geth.
func gethBlockNumber() (uint64, error) {
	type rpcResp struct {
		Result string `json:"result"`
	}
	client := &http.Client{Timeout: 3 * time.Second}
	payload := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	resp, err := client.Post("http://127.0.0.1:8545", "application/json",
		strings.NewReader(payload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var r rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, err
	}
	var n uint64
	fmt.Sscanf(r.Result, "0x%x", &n)
	return n, nil
}

// pidFile returns the path to the PID file for the running node.
func pidFile(dataDir string) string {
	return filepath.Join(dataDir, "chakram-node.pid")
}

func writePID(dataDir string, pid int) error {
	return os.WriteFile(pidFile(dataDir), []byte(fmt.Sprintf("%d", pid)), 0644)
}

func readPID(dataDir string) (int, error) {
	data, err := os.ReadFile(pidFile(dataDir))
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	return pid, err
}

// runCmd runs a command and returns its exit error.
func runCmd(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// defaultDataDir returns the platform-appropriate data directory.
func defaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appdata, "chakram-node")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "chakram-node")
	default: // linux + others
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "chakram-node")
		}
		return filepath.Join(os.Getenv("HOME"), ".chakram-node")
	}
}
