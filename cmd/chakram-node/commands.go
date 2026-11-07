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

// splitSeeds splits a comma-separated seed DNS list, trimming whitespace and
// dropping empty entries.
func splitSeeds(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ── start ──────────────────────────────────────────────────────────────────

func cmdStart(args []string) {
	networkURL, dataDir, rewardAddr, seedsDNS, noBrowser := startFlags(args)

	fmt.Printf("Chakram Node\n")
	fmt.Printf("  Data dir : %s\n", dataDir)
	fmt.Printf("\n")

	// ── 1. Peer discovery via DNS ──────────────────────────────────────────
	fmt.Printf("==> Peer Discovery\n")
	dnsNames := splitSeeds(seedsDNS)
	seeds := resolveSeedsDNS(dnsNames)

	// Always include the fallback network URL as an API source.
	if networkURL != "" {
		seeds.APIURLs = appendUniq(seeds.APIURLs, networkURL)
	}

	if len(seeds.CometPeers) > 0 || len(seeds.GethEnodes) > 0 {
		fmt.Printf("  DNS resolved  : %d comet peer(s), %d geth enode(s), %d API endpoint(s)\n",
			len(seeds.CometPeers), len(seeds.GethEnodes), len(seeds.APIURLs))
	} else {
		fmt.Printf("  DNS lookup returned no peers — will try fallback URL\n")
	}

	// ── 2. Bootstrap ──────────────────────────────────────────────────────
	fmt.Printf("\n==> Bootstrap\n")
	if err := bootstrap(dataDir, seeds.APIURLs); err != nil {
		fatalf("bootstrap failed: %v", err)
	}

	// ── 3. Enrich peer list from live API endpoints ────────────────────────
	// fetchNetworkInfo adds each API node's own comet peer + geth enode to
	// seeds (in case DNS didn't have them yet) and returns staking contract.
	fmt.Printf("  Querying network info...\n")
	stakingContract := fetchNetworkInfo(seeds.APIURLs, &seeds)

	if len(seeds.CometPeers) > 0 {
		for _, p := range seeds.CometPeers {
			fmt.Printf("  CometBFT peer    : %s\n", p)
		}
	}
	if len(seeds.GethEnodes) > 0 {
		for _, e := range seeds.GethEnodes {
			fmt.Printf("  Geth bootnode    : %s\n", e)
		}
	}
	if stakingContract != "" {
		fmt.Printf("  Staking contract : %s\n", stakingContract)
	}

	// ── 4. Chain setup ────────────────────────────────────────────────────
	chainID    := resolveChainID(dataDir)
	rewardAddr  = resolveRewardAddr(dataDir, rewardAddr)

	// ── 5. Start geth ────────────────────────────────────────────────────
	fmt.Printf("\n==> Starting geth\n")
	gethProc, err := startGeth(dataDir, filepath.Join(dataDir, "jwt.hex"),
		strings.Join(seeds.GethEnodes, ","))
	if err != nil {
		fatalf("%v", err)
	}

	_ = writePID(dataDir, os.Getpid())

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer waitCancel()
	fmt.Printf("  Waiting for geth...\n")
	if err := waitForGeth(waitCtx); err != nil {
		gethProc.Process.Kill()
		fatalf("geth did not start: %v", err)
	}
	fmt.Printf("  geth ready.\n")

	// ── 6. Start consensus ───────────────────────────────────────────────
	fmt.Printf("\n==> Starting consensus\n")
	fmt.Printf("  Chain ID     : %s\n", chainID)
	fmt.Printf("  Reward addr  : %s\n", rewardAddr)
	fmt.Printf("  Dashboard    : http://localhost:8080\n")
	fmt.Printf("\n")

	if !noBrowser {
		go func() {
			time.Sleep(2 * time.Second)
			openBrowser("http://localhost:8080")
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
	go func() {
		gethProc.Wait()
		fmt.Println("geth process exited.")
		cancel()
	}()

	if err := startConsensus(ctx, consensusCfg{
		dataDir:         dataDir,
		chainID:         chainID,
		rewardAddr:      rewardAddr,
		peers:           strings.Join(seeds.CometPeers, ","),
		stakingContract: stakingContract,
	}); err != nil {
		gethProc.Process.Kill()
		fatalf("consensus error: %v", err)
	}

	gethProc.Process.Kill()
	os.Remove(pidFile(dataDir))
}

// ── status ─────────────────────────────────────────────────────────────────

func cmdStatus(args []string) {
	_, dataDir, _, _, _ := startFlags(args)

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
	_, dataDir, _, _, _ := startFlags(args)

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

