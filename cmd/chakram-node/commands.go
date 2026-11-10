package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
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
		// Genesis node: resolve reward key first so the genesis alloc funds
		// the same address that will receive block rewards.
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			fatalf("create data dir: %v", err)
		}
		rewardAddr = resolveRewardAddr(dataDir, rewardAddr)

		fmt.Printf("==> Genesis Node Bootstrap\n")
		if err := bootstrapGenesisNode(dataDir, rewardAddr); err != nil {
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

		// ── 3.5. Auto-recover stale data, then auto-snapshot ─────────────────
		// If we're >500 blocks behind the network, the geth Engine API reorg
		// depth limit will reject new canonical chain updates. Wipe and re-import.
		checkAndRecoverStaleData(dataDir, seeds.APIURLs)

		// Before geth starts (no datadir lock yet), check if the seed nodes have
		// a snapshot. If this is the first run (no existing chain progress), import
		// it so we start at the snapshot height instead of syncing from genesis.
		if err := tryAutoSnapshot(dataDir, seeds.APIURLs); err != nil {
			fmt.Printf("  No snapshot available (%v) — syncing from genesis.\n", err)
		}
	}

	// ── 4. Chain setup ────────────────────────────────────────────────────
	chainID := resolveChainID(dataDir)
	rewardAddr = resolveRewardAddr(dataDir, rewardAddr) // no-op if already set above
	nodeKeyPath := filepath.Join(dataDir, "reward-key.json")

	// ── 5. Start geth ────────────────────────────────────────────────────
	fmt.Printf("\n==> Starting geth\n")
	gethProc, err := startGeth(dataDir, filepath.Join(dataDir, "jwt.hex"), seeds.GethEnodes)
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

	// Determine fast-sync strategy from snapshot state.
	// State-sync path: geth head stays at N; CometBFT state-syncs to N then
	// blocksyncs only N+1..current (seconds). Fallback: debug_setHead(0) so
	// CometBFT's blocksync replay from genesis is always FCU depth=1.
	trustFile := stateSyncTrustPath(dataDir)
	markerFile := snapshotMarkerPath(dataDir)

	var autoStateSync *stateSyncTrust
	if fileExists(trustFile) {
		data, err := os.ReadFile(trustFile)
		if err == nil {
			var t stateSyncTrust
			if json.Unmarshal(data, &t) == nil && t.Height > 0 {
				autoStateSync = &t
				fmt.Printf("  State-sync configured (height=%d) — CometBFT will skip pre-imported block replay.\n", t.Height)
			}
		}
		os.Remove(markerFile) // not needed in state-sync path
	} else if fileExists(markerFile) {
		fmt.Printf("  Resetting geth canonical head to genesis (snapshot fast-replay)...\n")
		if err := debugSetHeadToGenesis(); err != nil {
			gethProc.Process.Kill()
			fatalf("reset geth head after snapshot: %v\n  Wipe %s and retry.", err, dataDir)
		}
		os.Remove(markerFile)
		fmt.Printf("  Canonical head reset — CometBFT will fast-replay snapshot blocks.\n")
	}

	// If the state-sync trust file is present, geth has an imported snapshot.
	// Wait until geth's journal is fully loaded (eth_blockNumber reaches the
	// snapshot height) before CometBFT's Info() handshake queries it.
	// Without this, a brief window where geth reports height=0 or 1 triggers
	// Info()'s rollback path, corrupting the in-memory state before the handshake.
	if autoStateSync != nil {
		fmt.Printf("  Waiting for geth to report snapshot height %d...\n", autoStateSync.Height)
		heightCtx, heightCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer heightCancel()
		if err := waitForGethAtHeight(heightCtx, uint64(autoStateSync.Height)); err != nil {
			fmt.Printf("  Warning: geth height wait: %v — continuing anyway\n", err)
		} else {
			fmt.Printf("  Geth at height %d.\n", autoStateSync.Height)
		}
	}

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

	var autoHeight int64
	var autoHash, autoRPC string
	if autoStateSync != nil {
		autoHeight = autoStateSync.Height
		autoHash = autoStateSync.TrustHash
		autoRPC = autoStateSync.RPCServer
	}

	// Auto-enable snapshots on publicly reachable nodes (p2p-host set) so they
	// can serve state sync to nodes that need to resync quickly.
	if p2pHostFlag != "" && snapshotInterval == 0 {
		snapshotInterval = 200
	}

	// Restore last known peers so CometBFT dials them immediately at startup.
	// This ensures block sync activates before consensus mode takes over —
	// critical when the genesis node restarts and DNS only contains itself.
	for _, p := range loadLastPeers(dataDir) {
		seeds.CometPeers = appendUniq(seeds.CometPeers, p)
	}

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
		autoStateSyncHeight:  autoHeight,
		autoStateSyncHash:    autoHash,
		autoStateSyncRPC:     autoRPC,
		broadcastGethRPCURLs: seedGethRPCURLs(seeds.APIURLs),
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

	// Write the fallback marker (debug_setHead path).
	_ = os.WriteFile(snapshotMarkerPath(dataDir), []byte(fmt.Sprintf("%d", meta.Height)), 0644)

	// Try to fetch state-sync trust so the next start can skip full replay.
	if trust, err := fetchStateSyncTrust(fromURL, int64(meta.Height)); err == nil {
		if data, jerr := json.Marshal(trust); jerr == nil {
			_ = os.WriteFile(stateSyncTrustPath(dataDir), data, 0644)
			fmt.Printf("  State-sync trust fetched — 'chakram-node start' will state-sync to block %d.\n", meta.Height)
		}
	}

	fmt.Printf("\n==> Done. Run 'chakram-node start' to begin syncing from block %d.\n", meta.Height)
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

// stateSyncTrust holds the data needed to auto-configure CometBFT state-sync
// after a snapshot pre-import. Written by tryAutoSnapshot when the seed's
// CometBFT RPC is reachable; read by cmdStart to skip full blocksync replay.
type stateSyncTrust struct {
	Height    int64  `json:"height"`
	TrustHash string `json:"trust_hash"` // uppercase hex, no 0x prefix
	RPCServer string `json:"rpc_server"` // http://host:26657
}

func stateSyncTrustPath(dataDir string) string {
	return filepath.Join(dataDir, "state-sync-trust.json")
}

// fetchStateSyncTrust queries the seed's CometBFT RPC for the block hash at
// height. Returns the trust data needed to configure CometBFT state-sync.
// CometBFT's TrustHash is the block_id hash (uppercase hex) at TrustHeight.
func fetchStateSyncTrust(apiURL string, height int64) (*stateSyncTrust, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	rpcBase := "http://" + u.Hostname() + ":26657"
	endpoint := fmt.Sprintf("%s/block?height=%d", rpcBase, height)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("comet RPC unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("comet RPC returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Result struct {
			BlockID struct {
				Hash string `json:"hash"`
			} `json:"block_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode comet RPC response: %w", err)
	}
	hash := strings.ToUpper(result.Result.BlockID.Hash)
	if len(hash) != 64 {
		return nil, fmt.Errorf("unexpected block hash length %d (want 64)", len(hash))
	}
	return &stateSyncTrust{
		Height:    height,
		TrustHash: hash,
		RPCServer: rpcBase,
	}, nil
}

// snapshotMarkerPath returns the path to the "snapshot imported, needs head
// reset" marker file. Its presence means geth has been imported to height N
// but its canonical head has not yet been reset to genesis.
func snapshotMarkerPath(dataDir string) string {
	return filepath.Join(dataDir, "snapshot-needs-head-reset")
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

	// If a previous import already ran (e.g. via 'chakram-node snapshot'), skip
	// re-downloading. The head reset will happen in cmdStart after geth is up.
	if fileExists(snapshotMarkerPath(dataDir)) {
		return nil
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

	// Always write the fallback marker. cmdStart reads this if state-sync cannot
	// be configured; it calls debug_setHead(0) so CometBFT's blocksync replay
	// never triggers geth's 32-block engine API reorg depth limit.
	_ = os.WriteFile(snapshotMarkerPath(dataDir), []byte(fmt.Sprintf("%d", meta.Height)), 0644)

	// Try to fetch the CometBFT block hash so cmdStart can configure state-sync.
	// With state-sync, CometBFT jumps directly to the snapshot height instead of
	// replaying every block from genesis — sync takes seconds instead of minutes.
	if trust, err := fetchStateSyncTrust(snapshotBase, int64(meta.Height)); err == nil {
		if data, jerr := json.Marshal(trust); jerr == nil {
			_ = os.WriteFile(stateSyncTrustPath(dataDir), data, 0644)
			fmt.Printf("  State-sync trust fetched (height=%d) — CometBFT will start from snapshot.\n", meta.Height)
		}
	} else {
		fmt.Printf("  State-sync unavailable (%v) — will blocksync from genesis.\n", err)
	}

	fmt.Printf("  Geth import complete — %d blocks pre-loaded.\n\n", meta.Height)
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

// ── wallet ─────────────────────────────────────────────────────────────────

func cmdWallet(args []string) {
	fs := flag.NewFlagSet("wallet", flag.ExitOnError)
	var dataDir string
	fs.StringVar(&dataDir, "data-dir", defaultDataDir(), "data directory")
	_ = fs.Parse(args)

	keyPath := filepath.Join(dataDir, "reward-key.json")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wallet: key not found at %s\n  Start chakram-node once to generate it.\n", keyPath)
		os.Exit(1)
	}
	var k rewardKeyFile
	if err := json.Unmarshal(data, &k); err != nil || k.Address == "" {
		fmt.Fprintf(os.Stderr, "wallet: invalid key file at %s\n", keyPath)
		os.Exit(1)
	}
	fmt.Printf("Address     : %s\n", k.Address)
	fmt.Printf("Private key : 0x%s\n", k.PrivateKey)
}

// promptLine prints a label and reads a trimmed line from stdin.
func promptLine(label string) string {
	fmt.Print(label)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

// ── register ───────────────────────────────────────────────────────────────

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	var dataDir, stakeAmt, rewardAddr, moniker, website string
	fs.StringVar(&dataDir, "data-dir", defaultDataDir(), "data directory")
	fs.StringVar(&stakeAmt, "stake", "", "amount of CHK to stake (default: 1)")
	fs.StringVar(&rewardAddr, "reward", "", "reward address (default: node's own wallet)")
	fs.StringVar(&moniker, "moniker", "", "validator display name (optional)")
	fs.StringVar(&website, "website", "", "validator website (optional)")
	_ = fs.Parse(args)

	// Interactive prompts when no flags were supplied.
	if stakeAmt == "" && moniker == "" && website == "" {
		fmt.Println("Register this node as a validator.")
		fmt.Println()
		moniker = promptLine("Validator name (optional, Enter to skip): ")
		website = promptLine("Website      (optional, Enter to skip): ")
		s := promptLine("Stake in CHK (Enter for default [1]):   ")
		if s == "" {
			stakeAmt = "1"
		} else {
			stakeAmt = s
		}
		fmt.Println()
	}
	if stakeAmt == "" {
		stakeAmt = "1"
	}

	// Convert CHK → wei
	whole, frac, _ := strings.Cut(stakeAmt, ".")
	frac = (frac + "000000000000000000")[:18]
	stakeWei := fmt.Sprintf("%s%s", strings.TrimLeft(whole+"000000000000000000", "0"), "")
	wBig, ok1 := new(big.Int).SetString(whole, 10)
	fBig, ok2 := new(big.Int).SetString(frac, 10)
	if !ok1 || !ok2 {
		fmt.Fprintf(os.Stderr, "register: invalid stake amount %q\n", stakeAmt)
		os.Exit(1)
	}
	unit, _ := new(big.Int).SetString("1000000000000000000", 10)
	stakeWei = new(big.Int).Add(new(big.Int).Mul(wBig, unit), fBig).String()

	// Build JSON body
	type reqBody struct {
		RewardAddr string `json:"reward_addr,omitempty"`
		StakeWei   string `json:"stake_wei,omitempty"`
		Moniker    string `json:"moniker,omitempty"`
		Website    string `json:"website,omitempty"`
	}
	body := reqBody{StakeWei: stakeWei}
	if rewardAddr != "" {
		body.RewardAddr = rewardAddr
	}
	if moniker != "" {
		body.Moniker = moniker
	}
	if website != "" {
		body.Website = website
	}
	bodyJSON, _ := json.Marshal(body)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post("http://localhost:8080/api/staking/register", "application/json",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: could not reach node API — is chakram-node running?\n  %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		msg, _ := result["error"].(string)
		fmt.Fprintf(os.Stderr, "register: API error (%d): %s\n", resp.StatusCode, msg)
		os.Exit(1)
	}
	if txHash, ok := result["tx_hash"].(string); ok {
		fmt.Printf("Registered! tx: %s\n", txHash)
	} else {
		fmt.Printf("Response: %v\n", result)
	}
}

// ── unbond / withdraw ──────────────────────────────────────────────────────

func cmdUnbond(_ []string) {
	stakingPost("unbond", "http://localhost:8080/api/staking/unbond")
}

func cmdWithdraw(_ []string) {
	stakingPost("withdraw", "http://localhost:8080/api/staking/withdraw")
}

func stakingPost(name, url string) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: could not reach node API — is chakram-node running?\n  %v\n", name, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		msg, _ := result["error"].(string)
		fmt.Fprintf(os.Stderr, "%s: API error (%d): %s\n", name, resp.StatusCode, msg)
		os.Exit(1)
	}
	if txHash, ok := result["tx_hash"].(string); ok {
		fmt.Printf("OK — tx: %s\n", txHash)
	} else {
		fmt.Printf("Response: %v\n", result)
	}
}

// checkAndRecoverStaleData wipes geth-data and comet/data when the local chain
// is more than 500 blocks behind the network. This prevents geth's Engine API
// reorg depth limit from trapping a node that was offline for too long.
// After wiping, tryAutoSnapshot will re-import from the seed's latest snapshot.
func checkAndRecoverStaleData(dataDir string, apiURLs []string) {
	appStatePath := filepath.Join(dataDir, "comet", "data", "app_state.json")
	if !fileExists(appStatePath) {
		return // first run — tryAutoSnapshot handles this
	}

	data, err := os.ReadFile(appStatePath)
	if err != nil {
		return
	}
	var state struct {
		LastHeight int64 `json:"last_height"`
	}
	if json.Unmarshal(data, &state) != nil || state.LastHeight == 0 {
		return
	}

	networkHeight := fetchNetworkHeight(apiURLs)
	if networkHeight == 0 {
		return // seeds unreachable — don't wipe
	}

	const staleThreshold = 500
	gap := networkHeight - state.LastHeight
	if gap <= staleThreshold {
		return
	}

	fmt.Printf("\n==> Node is %d blocks behind (local=%d, network=%d)\n",
		gap, state.LastHeight, networkHeight)
	fmt.Printf("  Wiping stale chain data — will resync from latest snapshot.\n")

	cometData := filepath.Join(dataDir, "comet", "data")
	os.RemoveAll(filepath.Join(dataDir, "geth-data"))
	os.RemoveAll(cometData)
	_ = os.MkdirAll(cometData, 0755)
	_ = os.WriteFile(
		filepath.Join(cometData, "priv_validator_state.json"),
		[]byte(`{"height":"0","round":0,"step":0}`),
		0644,
	)
	os.Remove(snapshotMarkerPath(dataDir))
	os.Remove(stateSyncTrustPath(dataDir))
	fmt.Printf("  Wiped. Proceeding with snapshot re-import.\n\n")
}

// fetchNetworkHeight queries seed API endpoints and returns the highest reported
// chain height, or 0 if none are reachable.
func fetchNetworkHeight(apiURLs []string) int64 {
	client := &http.Client{Timeout: 5 * time.Second}
	var best int64
	for _, u := range apiURLs {
		resp, err := client.Get(u + "/api/status")
		if err != nil {
			continue
		}
		var status struct {
			Height int64 `json:"height"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if decodeErr == nil && status.Height > best {
			best = status.Height
		}
	}
	return best
}

// seedGethRPCURLs derives geth RPC URLs from seed API URLs by replacing the
// "node." subdomain with "rpc." (e.g. https://node.chakram.one → https://rpc.chakram.one).
// These are used to broadcast signed transactions since local geth has no peers.
func seedGethRPCURLs(apiURLs []string) []string {
	var out []string
	for _, u := range apiURLs {
		rpc := strings.Replace(u, "://node.", "://rpc.", 1)
		if rpc != u { // only add if the replacement actually changed something
			out = append(out, rpc)
		}
	}
	return out
}


