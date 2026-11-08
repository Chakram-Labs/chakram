package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
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
	networkURL, dataDir, rewardAddr, seedsDNS, stakingContractFlag, p2pHostFlag, noBrowser,
		snapshotInterval, fastSyncHeight, fastSyncHash, stateSyncRPCServers, genesisNode := startFlags(args)

	fmt.Printf("Chakram Node\n")
	fmt.Printf("  Data dir : %s\n", dataDir)
	fmt.Printf("\n")

	var seeds seedList
	var stakingContract string

	if genesisNode {
		// Genesis node: create own genesis without fetching from network.
		fmt.Printf("==> Genesis Node Bootstrap\n")
		if err := bootstrapGenesisNode(dataDir); err != nil {
			fatalf("genesis bootstrap failed: %v", err)
		}
		stakingContract = stakingContractFlag
	} else {
		// ── 1. Peer discovery via DNS ──────────────────────────────────────────
		fmt.Printf("==> Peer Discovery\n")
		dnsNames := splitSeeds(seedsDNS)
		seeds = resolveSeedsDNS(dnsNames)

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
		fmt.Printf("  Querying network info...\n")
		stakingContract = fetchNetworkInfo(seeds.APIURLs, &seeds)
		if stakingContractFlag != "" {
			stakingContract = stakingContractFlag
		}

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

		// ── 3.5. Auto-snapshot on first run ───────────────────────────────────
		// Before geth starts (no datadir lock yet), check if the seed nodes have
		// a snapshot. If this is the first run (no existing chain progress), import
		// it so we start at the snapshot height instead of syncing from genesis.
		if err := tryAutoSnapshot(dataDir, seeds.APIURLs); err != nil {
			fmt.Printf("  No snapshot available (%v) — syncing from genesis.\n", err)
		}
	}

	// ── 4. Chain setup ────────────────────────────────────────────────────
	chainID := resolveChainID(dataDir)
	if genesisNode && rewardAddr == "" {
		rewardAddr = "0xdeF22dBC999aD395527205C90d7922357378CAe0"
	}
	rewardAddr = resolveRewardAddr(dataDir, rewardAddr)
	nodeKeyPath := filepath.Join(dataDir, "reward-key.json")

	// ── 5. Start geth ────────────────────────────────────────────────────
	fmt.Printf("\n==> Starting geth\n")
	gethProc, err := startGeth(dataDir, filepath.Join(dataDir, "jwt.hex"))
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

	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))
	if err := startConsensus(ctx, consensusCfg{
		dataDir:              dataDir,
		chainID:              chainID,
		rewardAddr:           rewardAddr,
		peers:                strings.Join(seeds.CometPeers, ","),
		stakingContract:      stakingContract,
		nodeKeyPath:          nodeKeyPath,
		gethBin:              gethBin,
		p2pHost:              p2pHostFlag,
		snapshotInterval:     snapshotInterval,
		fastSyncHeight:       fastSyncHeight,
		fastSyncHash:         fastSyncHash,
		stateSyncEnable:      stateSyncRPCServers != "" && fastSyncHash != "",
		stateSyncRPCServers:  stateSyncRPCServers,
		stateSyncTrustHeight: fastSyncHeight,
		stateSyncTrustHash:   fastSyncHash,
	}); err != nil {
		gethProc.Process.Kill()
		fatalf("consensus error: %v", err)
	}

	gethProc.Process.Kill()
	os.Remove(pidFile(dataDir))
}

// ── status ─────────────────────────────────────────────────────────────────

func cmdStatus(args []string) {
	_, dataDir, _, _, _, _, _, _, _, _, _, _ := startFlags(args)

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
	_, dataDir, _, _, _, _, _, _, _, _, _, _ := startFlags(args)

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

// ── snapshot ───────────────────────────────────────────────────────────────

// cmdSnapshot downloads a snapshot from a seed node, imports it into geth,
// and writes app_state.json so the next 'start' begins at the snapshot height.
// Run this BEFORE 'chakram-node start' — geth must not be running.
func cmdSnapshot(args []string) {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	var fromURL, dataDir string
	fs.StringVar(&fromURL, "from", "https://node.chakram.one", "seed node URL to download snapshot from")
	fs.StringVar(&dataDir, "data-dir", defaultDataDir(), "data directory")
	_ = fs.Parse(args)

	fmt.Printf("Chakram snapshot download\n")
	fmt.Printf("  Data dir : %s\n  From     : %s\n\n", dataDir, fromURL)

	// Bootstrap first to ensure geth binary + genesis exist.
	apiURLs := []string{fromURL}
	if err := bootstrap(dataDir, apiURLs); err != nil {
		fatalf("bootstrap: %v", err)
	}

	// Fetch snapshot metadata.
	fmt.Printf("==> Fetching snapshot info...\n")
	meta, err := fetchSnapshotMeta(fromURL)
	if err != nil {
		fatalf("snapshot meta: %v", err)
	}
	fmt.Printf("  Height : %d\n  Hash   : %s\n  Size   : %s\n",
		meta.Height, meta.Hash, formatBytes(meta.SizeBytes))

	// Download chain.rlp.
	chainFile := filepath.Join(dataDir, "snapshot-import.rlp")
	fmt.Printf("\n==> Downloading chain export (%s)...\n", formatBytes(meta.SizeBytes))
	if err := downloadFile(fromURL+"/api/snapshot/latest/chain.rlp", chainFile); err != nil {
		fatalf("download: %v", err)
	}
	fmt.Printf("  Downloaded.\n")

	// Run geth import.
	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))
	gethDataDir := filepath.Join(dataDir, "geth-data")
	fmt.Printf("\n==> Importing into geth (this may take several minutes)...\n")
	if err := runGethImportCmd(gethBin, gethDataDir, chainFile); err != nil {
		os.Remove(chainFile)
		fatalf("geth import: %v", err)
	}
	os.Remove(chainFile)
	fmt.Printf("  Import complete.\n")

	// Do NOT set app_state.json here — see tryAutoSnapshot for the reason.
	// CometBFT will blocksync and fast-replay the pre-imported geth blocks.

	fmt.Printf("\n==> Done. Run 'chakram-node start' — will fast-replay %d pre-imported blocks.\n", meta.Height)
}

type snapshotMetaHTTP struct {
	Height    uint64 `json:"height"`
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
}

func fetchSnapshotMeta(baseURL string) (*snapshotMetaHTTP, error) {
	v, err := httpGetJSON(baseURL + "/api/snapshot/latest")
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(v)
	var m snapshotMetaHTTP
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m.Height == 0 {
		return nil, fmt.Errorf("no snapshot available on %s", baseURL)
	}
	return &m, nil
}

func runGethImportCmd(gethBin, gethDataDir, chainFile string) error {
	cmd := exec.Command(gethBin, "--datadir", gethDataDir, "import", chainFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// tryAutoSnapshot checks if a snapshot is available from any seed node and,
// on first run (no existing chain progress), downloads and imports it so the
// node starts at the snapshot height instead of syncing from genesis.
// Geth must NOT be running when this is called (import requires the datadir lock).
func tryAutoSnapshot(dataDir string, apiURLs []string) error {
	// Skip if the node already has chain progress.
	appStatePath := filepath.Join(dataDir, "comet", "data", "app_state.json")
	if fileExists(appStatePath) {
		return fmt.Errorf("existing chain state found")
	}

	// Find the first seed that has a snapshot.
	var meta *snapshotMetaHTTP
	var snapshotBase string
	for _, url := range apiURLs {
		m, err := fetchSnapshotMeta(url)
		if err == nil && m != nil && m.Height > 0 {
			meta = m
			snapshotBase = url
			break
		}
	}
	if meta == nil {
		return fmt.Errorf("no snapshot available on seed nodes")
	}

	fmt.Printf("\n==> Auto-snapshot (height %d, %s)\n", meta.Height, formatBytes(meta.SizeBytes))
	fmt.Printf("  Downloading from %s...\n", snapshotBase)

	chainFile := filepath.Join(dataDir, "snapshot-import.rlp")
	if err := downloadFile(snapshotBase+"/api/snapshot/latest/chain.rlp", chainFile); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))
	gethDataDir := filepath.Join(dataDir, "geth-data")
	fmt.Printf("  Importing into geth (may take a moment)...\n")
	if err := runGethImportCmd(gethBin, gethDataDir, chainFile); err != nil {
		os.Remove(chainFile)
		return fmt.Errorf("geth import: %w", err)
	}
	os.Remove(chainFile)

	// Do NOT call applyFastSync here. Setting app_state.json to height N while
	// CometBFT's block store is empty (storeHeight=0) causes CometBFT to panic
	// trying to replay N blocks it doesn't have.
	//
	// Instead, leave app_state at height 0. CometBFT will blocksync normally
	// and call FinalizeBlock for blocks 1..N. Since geth already has these blocks
	// from the import, each engine_newPayload returns VALID immediately and
	// engine_FCU just advances the head — far faster than re-executing EVM state.
	fmt.Printf("  Geth import complete — CometBFT will fast-replay %d pre-imported blocks.\n\n", meta.Height)
	return nil
}

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

type rewardKeyFile struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

// generateRewardKey creates a new secp256k1 keypair and returns the EVM address + hex private key.
func generateRewardKey() (*rewardKeyFile, error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	uncompressed := privKey.PubKey().SerializeUncompressed() // 65 bytes: 0x04 + X + Y
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hash := h.Sum(nil)
	address := "0x" + hex.EncodeToString(hash[12:]) // last 20 bytes
	return &rewardKeyFile{
		Address:    address,
		PrivateKey: hex.EncodeToString(privKey.Serialize()),
	}, nil
}

func resolveRewardAddr(dataDir, addr string) string {
	if addr != "" {
		return addr
	}

	keyPath := filepath.Join(dataDir, "reward-key.json")
	if fileExists(keyPath) {
		data, err := os.ReadFile(keyPath)
		if err == nil {
			var k rewardKeyFile
			if json.Unmarshal(data, &k) == nil && k.Address != "" {
				return k.Address
			}
		}
	}

	k, err := generateRewardKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not generate EVM key: %v\n", err)
		return "0x0000000000000000000000000000000000000000"
	}
	data, _ := json.MarshalIndent(k, "", "  ")
	if err := os.WriteFile(keyPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not save reward key: %v\n", err)
	} else {
		fmt.Printf("  Generated node EVM wallet : %s\n", k.Address)
		fmt.Printf("  Key saved to              : %s\n", keyPath)
	}
	return k.Address
}

