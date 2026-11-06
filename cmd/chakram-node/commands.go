package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── start ──────────────────────────────────────────────────────────────────

func cmdStart(args []string) {
	networkURL, dataDir, rewardAddr, noBrowser := startFlags(args)

	fmt.Printf("Chakram Node\n")
	fmt.Printf("  Data dir : %s\n", dataDir)
	fmt.Printf("  Network  : %s\n", networkURL)
	fmt.Printf("\n")

	// Bootstrap: download geth, fetch genesis, init chain data.
	fmt.Printf("==> Bootstrap\n")
	if err := bootstrap(dataDir, networkURL); err != nil {
		fatalf("bootstrap failed: %v", err)
	}

	// Fetch peer info (non-fatal — node can still start without peers).
	var cometPeer, gethEnode string
	fmt.Printf("  Fetching peer info...\n")
	if p, e, err := fetchPeerInfo(networkURL); err == nil {
		cometPeer = p
		gethEnode = e
		fmt.Printf("  CometBFT peer : %s\n", cometPeer)
		fmt.Printf("  Geth bootnode : %s\n", gethEnode)
	} else {
		fmt.Printf("  Warning: could not fetch peer info: %v\n", err)
	}

	// Resolve chain ID from downloaded genesis.
	chainID := resolveChainID(dataDir)

	// Resolve reward address.
	rewardAddr = resolveRewardAddr(dataDir, rewardAddr)

	fmt.Printf("\n==> Starting geth\n")
	gethProc, err := startGeth(dataDir, filepath.Join(dataDir, "jwt.hex"), gethEnode)
	if err != nil {
		fatalf("%v", err)
	}

	// Write PID so `chakram-node stop` can find the process group.
	_ = writePID(dataDir, os.Getpid())

	// Wait for geth RPC to be ready.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer waitCancel()
	fmt.Printf("  Waiting for geth...\n")
	if err := waitForGeth(waitCtx); err != nil {
		gethProc.Process.Kill()
		fatalf("geth did not start: %v", err)
	}
	fmt.Printf("  geth ready.\n")

	fmt.Printf("\n==> Starting consensus\n")
	fmt.Printf("  Chain ID     : %s\n", chainID)
	fmt.Printf("  Reward addr  : %s\n", rewardAddr)
	fmt.Printf("  Dashboard    : http://localhost:8080\n")
	fmt.Printf("\n")

	if !noBrowser {
		go func() {
			time.Sleep(2 * time.Second)
			if webUI() != nil {
				openBrowser("http://localhost:8080")
			} else {
				// No embedded web UI — open the public explorer.
				// The local node still provides the API at localhost:8080.
				openBrowser("http://localhost:8080")
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan os.Signal, 1)
	notifySignals(quit)
	go func() {
		<-quit
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Also cancel if geth exits unexpectedly.
	go func() {
		gethProc.Wait()
		fmt.Println("geth process exited.")
		cancel()
	}()

	if err := startConsensus(ctx, consensusCfg{
		dataDir:    dataDir,
		chainID:    chainID,
		rewardAddr: rewardAddr,
		peers:      cometPeer,
	}); err != nil {
		gethProc.Process.Kill()
		fatalf("consensus error: %v", err)
	}

	gethProc.Process.Kill()
	os.Remove(pidFile(dataDir))
}

// ── status ─────────────────────────────────────────────────────────────────

func cmdStatus(args []string) {
	_, dataDir, _, _ := startFlags(args)

	pid, err := readPID(dataDir)
	if err != nil {
		fmt.Println("Status: not running")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil || !isProcessRunning(proc) {
		fmt.Println("Status: not running (stale PID)")
		os.Remove(pidFile(dataDir))
		return
	}

	fmt.Printf("Status: running (pid %d)\n", pid)

	// Query local geth for block height.
	if n, err := gethBlockNumber(); err == nil {
		fmt.Printf("Block height: %d\n", n)
	}

	// Query node-info endpoint.
	type nodeInfo struct {
		CometPeer string `json:"comet_peer"`
		GethEnode string `json:"geth_enode"`
	}
	resp, err := httpGetJSON("http://localhost:8080/api/node-info")
	if err == nil {
		body, _ := json.Marshal(resp)
		var ni nodeInfo
		_ = json.Unmarshal(body, &ni)
		if ni.CometPeer != "" {
			fmt.Printf("Node ID: %s\n", strings.Split(ni.CometPeer, "@")[0])
		}
	}

	fmt.Printf("Dashboard: http://localhost:8080\n")
}

// ── stop ───────────────────────────────────────────────────────────────────

func cmdStop(args []string) {
	_, dataDir, _, _ := startFlags(args)

	pid, err := readPID(dataDir)
	if err != nil {
		fmt.Println("chakram-node is not running.")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("chakram-node is not running.")
		return
	}

	if err := stopProcess(proc); err != nil {
		fmt.Fprintf(os.Stderr, "failed to signal process %d: %v\n", pid, err)
		os.Exit(1)
	}
	fmt.Printf("Stopping pid %d...\n", pid)
	os.Remove(pidFile(dataDir))
}

// ── helpers ────────────────────────────────────────────────────────────────

func resolveChainID(dataDir string) string {
	genesisPath := filepath.Join(dataDir, "comet", "config", "genesis.json")
	data, err := os.ReadFile(genesisPath)
	if err != nil {
		return "chakram-testnet-1"
	}
	var g struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(data, &g); err != nil || g.ChainID == "" {
		return "chakram-testnet-1"
	}
	return g.ChainID
}

func resolveRewardAddr(dataDir, addr string) string {
	if addr != "" {
		return addr
	}

	// Try to load from saved key.
	keyPath := filepath.Join(dataDir, "reward-key.json")
	if fileExists(keyPath) {
		data, err := os.ReadFile(keyPath)
		if err == nil {
			var k struct {
				Address string `json:"address"`
			}
			if json.Unmarshal(data, &k) == nil && k.Address != "" {
				return k.Address
			}
		}
	}

	// Generate a new one — just use zero address as placeholder.
	// Real staking requires a funded address; for now the node syncs fine with zero.
	return "0x0000000000000000000000000000000000000000"
}

