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
// bootnodes is a list of enode:// URLs used to seed devp2p peer discovery.
// Passing an empty slice starts geth without any bootstrap peers (genesis node).
func startGeth(dataDir, jwtPath string, bootnodes []string) (*exec.Cmd, error) {
	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))

	args := []string{
		"--datadir", filepath.Join(dataDir, "geth-data"),
		"--networkid", "11640",
		// Geth is a pure execution engine — block ordering comes from CometBFT
		// via the Engine API. P2P discovery is enabled so validators gossip
		// pending transactions to each other; whoever proposes the next block
		// will find the tx in its mempool regardless of who submitted it.
		// --syncmode full prevents snap-sync races during initial Engine API replay.
		"--syncmode", "full",
		"--gcmode", "full",
		"--history.transactions", "0",
		"--verbosity", "1", // 0=silent 1=error 2=warn 3=info(default)

		// Disable txpool journal persistence — a stale journal across restarts
		// can cause nonce gaps that prevent the next signed tx from landing on-chain.
		"--txpool.journal", "",
		// Plain RPC (for chakram consensus + local API)
		"--http",
		"--http.addr", "127.0.0.1",
		"--http.port", "8545",
		"--http.api", "eth,net,web3,admin,debug",
		"--http.vhosts", "localhost,127.0.0.1",
		// Engine API (for chakram consensus via JWT)
		"--authrpc.addr", "127.0.0.1",
		"--authrpc.port", "8551",
		"--authrpc.jwtsecret", jwtPath,
		"--authrpc.vhosts", "localhost,127.0.0.1",
	}

	if len(bootnodes) > 0 {
		args = append(args, "--bootnodes", strings.Join(bootnodes, ","))
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

// waitForGethAtHeight polls eth_blockNumber until geth reports >= minHeight or
// the context is cancelled. Used after snapshot imports to ensure geth has fully
// loaded the journal before CometBFT's Info() handshake queries the block number.
func waitForGethAtHeight(ctx context.Context, minHeight uint64) error {
	type rpcResp struct {
		Result string `json:"result"`
	}
	client := &http.Client{Timeout: 2 * time.Second}
	payload := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for geth height %d", minHeight)
		default:
		}
		resp, err := client.Post("http://127.0.0.1:8545", "application/json",
			strings.NewReader(payload))
		if err == nil {
			var r rpcResp
			json.NewDecoder(resp.Body).Decode(&r)
			resp.Body.Close()
			var n uint64
			fmt.Sscanf(r.Result, "0x%x", &n)
			if n >= minHeight {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// debugSetHeadToGenesis calls debug_setHead("0x0") on geth's HTTP RPC, rolling
// the canonical chain head back to genesis. All imported blocks remain in the
// database as non-canonical; the engine API can re-canonize them one by one
// via FCU without ever exceeding the 32-block reorg depth limit.
func debugSetHeadToGenesis() error {
	client := &http.Client{Timeout: 30 * time.Second}
	payload := `{"jsonrpc":"2.0","method":"debug_setHead","params":["0x0"],"id":1}`
	resp, err := client.Post("http://127.0.0.1:8545", "application/json",
		strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("debug_setHead RPC: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("debug_setHead decode: %w", err)
	}
	if result.Error != nil {
		return fmt.Errorf("debug_setHead: %s", result.Error.Message)
	}
	return nil
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
