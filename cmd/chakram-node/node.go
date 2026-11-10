package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmtconfig "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/syndtr/goleveldb/leveldb"

	chakramabci "chakram/abci"
	chakramapi "chakram/api"
	"chakram/engine"
)

// startConsensus initialises and starts the CometBFT node + chakram API.
// It blocks until ctx is cancelled or an error occurs.
func startConsensus(ctx context.Context, cfg consensusCfg) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cometHome := filepath.Join(cfg.dataDir, "comet")
	jwtPath := filepath.Join(cfg.dataDir, "jwt.hex")

	// Ensure CometBFT keys exist.
	if err := ensureChakramKeys(cometHome, cfg.chainID); err != nil {
		return fmt.Errorf("init comet keys: %w", err)
	}

	eng, err := engine.NewClientFromFile("http://127.0.0.1:8551", jwtPath)
	if err != nil {
		return fmt.Errorf("engine client: %w", err)
	}

	// Derive our CometBFT validator address before creating the app so it can
	// gate payload pre-building to active validators only.
	validatorAddr := loadValidatorAddress(cometHome)

	baseApp, err := chakramabci.NewChakramApp(eng, chakramabci.Config{
		Home:                cometHome,
		RewardAddress:       cfg.rewardAddr,
		GethRPCURL:          "http://127.0.0.1:8545",
		GethDataDir:         filepath.Join(cfg.dataDir, "geth-data"),
		GethBin:             cfg.gethBin,
		SnapshotInterval:    cfg.snapshotInterval,
		ValidatorAddress:    validatorAddr,
		StakingContractAddr: cfg.stakingContract,
	}, logger)
	if err != nil {
		return fmt.Errorf("create ABCI app: %w", err)
	}

	cmtCfg := cmtconfig.DefaultConfig()
	cmtCfg.SetRoot(cometHome)

	// Load node key early so we can filter our own address from the peer list.
	// ensureChakramKeys (called above) guarantees the file already exists.
	nodeKey, err := p2p.LoadOrGenNodeKey(cmtCfg.NodeKeyFile())
	if err != nil {
		return fmt.Errorf("load node key: %w", err)
	}

	cmtCfg.Mempool.Type = cmtconfig.MempoolTypeNop
	cmtCfg.P2P.ListenAddress = "tcp://0.0.0.0:26656"
	cmtCfg.P2P.AllowDuplicateIP = true
	cmtCfg.RPC.ListenAddress = "tcp://0.0.0.0:26657"
	if cfg.peers != "" {
		// Filter own node ID — CometBFT would skip it anyway, but keeping it
		// wastes the PersistentPeers slot (the genesis node sees itself in DNS).
		peers := filterSelf(cfg.peers, string(nodeKey.ID()))
		if peers != "" {
			// Use as both seeds (PEX discovery) and persistent peers (immediate
			// dial on start so block sync can activate before consensus takes over).
			cmtCfg.P2P.Seeds = peers
			cmtCfg.P2P.PersistentPeers = peers
		}
	}
	cmtCfg.Consensus.TimeoutPropose = 2 * time.Second
	cmtCfg.Consensus.TimeoutPrevote = 1 * time.Second
	cmtCfg.Consensus.TimeoutPrecommit = 1 * time.Second
	cmtCfg.Consensus.TimeoutCommit = 500 * time.Millisecond
	cmtCfg.Consensus.CreateEmptyBlocks = true

	// Auto state-sync: configured automatically after a snapshot pre-import.
	// Geth head stays at N (not reset to genesis); CometBFT state-syncs to N,
	// then blocksyncs only N+1..current — seconds instead of minutes.
	if cfg.autoStateSyncHeight > 0 && cfg.autoStateSyncHash != "" && cfg.autoStateSyncRPC != "" {
		cmtCfg.StateSync.Enable = true
		// CometBFT requires ≥2 RPC server entries for the light client. Passing
		// the same seed twice works: primary and witness both query the same node.
		cmtCfg.StateSync.RPCServers = []string{cfg.autoStateSyncRPC, cfg.autoStateSyncRPC}
		cmtCfg.StateSync.TrustHeight = cfg.autoStateSyncHeight
		cmtCfg.StateSync.TrustHash = cfg.autoStateSyncHash
		cmtCfg.StateSync.TrustPeriod = 168 * time.Hour
		// CometBFT state-sync rejects the nop mempool reactor; flood works fine
		// since Chakram's EVM txs route through geth directly, not CometBFT.
		cmtCfg.Mempool.Type = cmtconfig.MempoolTypeFlood
	}

	// Manual state-sync (--rpc-servers / --trusted-hash flags) overrides auto.
	if cfg.stateSyncEnable && cfg.stateSyncRPCServers != "" {
		cmtCfg.StateSync.Enable = true
		cmtCfg.StateSync.RPCServers = strings.Split(cfg.stateSyncRPCServers, ",")
		cmtCfg.StateSync.TrustHeight = cfg.stateSyncTrustHeight
		cmtCfg.StateSync.TrustHash = cfg.stateSyncTrustHash
		cmtCfg.StateSync.TrustPeriod = 168 * time.Hour
	}

	// If CometBFT StateSync is enabled and the blockstore is empty, pre-bootstrap
	// state.db via light client before NewNode runs. CometBFT's internal state-sync
	// skips nodes where the genesis validator set has only a single entry
	// (onlyValidatorIsUs guard), so genesis nodes can never state-sync natively.
	// BootstrapState bypasses that check: it writes state.db at TrustHeight with the
	// correct multi-validator set and AppHash from the light client, so the
	// subsequent Info() handshake succeeds and blocksync starts from TrustHeight+1.
	if cmtCfg.StateSync.Enable && cmtCfg.StateSync.TrustHeight > 0 {
		blockstoreDB := filepath.Join(cometHome, "data", "blockstore.db")
		if !hasLevelDBData(blockstoreDB) {
			fmt.Printf("  Bootstrapping CometBFT state to height %d...\n", cmtCfg.StateSync.TrustHeight)

			// CometBFT writes genesis state (validators != nil, LastBlockHeight = 0)
			// into state.db on first init. BootstrapState's "state not empty" check
			// rejects this even though no blocks have been committed. With an empty
			// blockstore it is safe to wipe state.db — BootstrapState re-derives
			// genesis from genesis.json and then writes the target-height state.
			_ = os.RemoveAll(filepath.Join(cometHome, "data", "state.db"))

			// Pass nil appHash to let the light client verify its own state; the
			// handshake will confirm the AppHash when Info() is called.
			bsErr := node.BootstrapState(ctx, cmtCfg, cmtconfig.DefaultDBProvider,
				uint64(cmtCfg.StateSync.TrustHeight), nil)
			switch {
			case bsErr == nil:
				fmt.Printf("  CometBFT state bootstrapped to height %d.\n", cmtCfg.StateSync.TrustHeight)
				// Remove trust file so next restart skips bootstrap — stores are
				// no longer empty and CometBFT would refuse to re-bootstrap anyway.
				_ = os.Remove(filepath.Join(cfg.dataDir, "state-sync-trust.json"))
			case strings.Contains(bsErr.Error(), "not empty"):
				// Stores already bootstrapped from a previous run — OK to continue.
			default:
				fmt.Printf("  Warning: state bootstrap failed: %v\n", bsErr)
			}
		}
	}

	// Auto-repair blockstore metadata if the "blockStore" key was lost in a
	// crash. Must run before node.NewNode() opens the blockstore.
	repairBlockstoreIfNeeded(cometHome)

	// Wipe stale WAL on every startup. A partial WAL from a crashed or stalled
	// consensus round can cause state.db inconsistencies on restart. The blockstore
	// is unaffected; block sync re-downloads any missing committed blocks.
	_ = os.RemoveAll(filepath.Join(cometHome, "data", "cs.wal"))

	cmtconfig.EnsureRoot(cometHome)

	keyFile := cmtCfg.PrivValidatorKeyFile()
	stateFile := cmtCfg.PrivValidatorStateFile()

	// Reset sign state so the validator can vote at the next height after any
	// restart. The WAL is wiped above, so consensus always re-runs the current
	// height from scratch. Without this reset, a validator stopped mid-height
	// retains a signed vote for the old block hash; when consensus proposes a
	// new block (different hash, different timestamp) the double-signing guard
	// rejects every vote and the chain halts permanently. Setting step=0 at the
	// last committed height still prevents signing for already-committed heights
	// while letting the validator participate freely in the next round.
	resetValidatorSignState(stateFile, appStateLastHeight(cometHome))

	var pv *privval.FilePV
	if fileExists(keyFile) {
		pv = privval.LoadFilePV(keyFile, stateFile)
	} else {
		pv = privval.GenFilePV(keyFile, stateFile)
		pv.Save()
	}
	// nodeKey is already loaded above (early load for self-filtering).

	// Start the API server before node.NewNode() — the handshake replay that
	// happens inside NewNode can take several minutes while FinalizeBlock is
	// called for each missing block. The API must be reachable during that
	// window so the dashboard and health checks don't time out.
	apiCfg := chakramapi.Config{
		ListenAddr:           "0.0.0.0:8080",
		CometRPCURL:          "http://127.0.0.1:26657",
		GethRPCURL:           "http://127.0.0.1:8545",
		HomeDir:              cometHome,
		StaticDir:            cfg.staticDir,
		StaticFS:             webUI(),
		StakingContractAddr:  cfg.stakingContract,
		NodeKeyPath:          cfg.nodeKeyPath,
		RewardAddress:        cfg.rewardAddr,
		P2PHost:              cfg.p2pHost,
		BroadcastGethRPCURLs: cfg.broadcastGethRPCURLs,
	}
	apiSrv := chakramapi.New(apiCfg, logger)
	if err := apiSrv.Start(); err != nil {
		logger.Warn("node API unavailable", "err", err)
	}

	cmtNode, err := node.NewNode(
		cmtCfg,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(baseApp),
		node.DefaultGenesisDocProviderFunc(cmtCfg),
		cmtconfig.DefaultDBProvider,
		node.DefaultMetricsProvider(cmtCfg.Instrumentation),
		cmtlog.NewFilter(cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stderr)), cmtlog.AllowWarn()),
	)
	if err != nil {
		return fmt.Errorf("create CometBFT node: %w", err)
	}

	if err := cmtNode.Start(); err != nil {
		return fmt.Errorf("start CometBFT node: %w", err)
	}
	logger.Info("chakram consensus started", "peers", cfg.peers)

	// Persist connected peers so the next restart can dial them immediately
	// and block sync activates before consensus mode takes over.
	go savePeersPeriodically(ctx, cfg.dataDir)

	<-ctx.Done()

	logger.Info("shutting down")
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiSrv.Stop(stopCtx)
	_ = cmtNode.Stop()
	cmtNode.Wait()
	return nil
}

type consensusCfg struct {
	dataDir         string
	chainID         string
	rewardAddr      string
	peers           string
	staticDir       string
	stakingContract string
	nodeKeyPath     string
	p2pHost         string

	// Snapshot / state-sync support.
	gethBin          string // path to geth binary
	snapshotInterval int64  // 0 = disabled
	// When > 0, write app_state.json to this height before starting CometBFT so
	// blocksync begins from here (geth must already have the data at this height).
	fastSyncHeight int64
	fastSyncHash   string // 0x-prefixed 32-byte EVM block hash at fastSyncHeight
	// CometBFT StateSync config (requires external RPC servers for hash verification).
	stateSyncEnable      bool
	stateSyncRPCServers  string // comma-separated comet RPC URLs
	stateSyncTrustHash   string // 0x-prefixed app_hash
	stateSyncTrustHeight int64

	// Auto state-sync: populated by tryAutoSnapshot when it successfully fetches
	// the trust hash from the seed's CometBFT RPC. When set, CometBFT state-syncs
	// to the snapshot height and only blocksyncs the remaining N+1..current blocks.
	autoStateSyncHeight int64
	autoStateSyncHash   string // uppercase hex, no 0x prefix
	autoStateSyncRPC    string // http://host:26657

	// broadcastGethRPCURLs are seed/public geth RPC endpoints that signed
	// transactions are rebroadcast to. Required because local geth has no peers.
	broadcastGethRPCURLs []string
}

// loadValidatorAddress returns this node's CometBFT validator address (uppercase
// hex, no 0x) by reading the privval key file. Returns "" if not found.
func loadValidatorAddress(cometHome string) string {
	keyFile := filepath.Join(cometHome, "config", "priv_validator_key.json")
	if !fileExists(keyFile) {
		return ""
	}
	stateFile := filepath.Join(cometHome, "data", "priv_validator_state.json")
	pv := privval.LoadFilePV(keyFile, stateFile)
	pubKey, err := pv.GetPubKey()
	if err != nil {
		return ""
	}
	return pubKey.Address().String()
}

// ensureChakramKeys generates CometBFT keys if not present and writes
// the comet genesis + config files needed to start.
func ensureChakramKeys(cometHome, chainID string) error {
	if err := os.MkdirAll(filepath.Join(cometHome, "config"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cometHome, "data"), 0755); err != nil {
		return err
	}

	cmtCfg := cmtconfig.DefaultConfig()
	cmtCfg.SetRoot(cometHome)

	keyFile := cmtCfg.PrivValidatorKeyFile()
	stateFile := cmtCfg.PrivValidatorStateFile()

	if !fileExists(keyFile) {
		pv := privval.GenFilePV(keyFile, stateFile)
		pv.Save()
		pubKey, _ := pv.GetPubKey()
		fmt.Printf("  Generated validator key: %s\n", pubKey.Address())
	}

	if !fileExists(cmtCfg.NodeKeyFile()) {
		if _, err := p2p.LoadOrGenNodeKey(cmtCfg.NodeKeyFile()); err != nil {
			return err
		}
	}

	// Write genesis.json if the genesis file exists in comet/config/ already
	// (placed there by ensureGenesis). If not, write a placeholder so CometBFT
	// doesn't crash — the real genesis must be fetched first.
	genesisPath := cmtCfg.BaseConfig.GenesisFile()
	if !fileExists(genesisPath) {
		pv := privval.LoadFilePV(keyFile, stateFile)
		pubKey, err := pv.GetPubKey()
		if err != nil {
			return err
		}
		genesis := map[string]any{
			"genesis_time":     time.Now().UTC().Format(time.RFC3339),
			"chain_id":         chainID,
			"initial_height":   "1",
			"consensus_params": defaultConsensusParams(),
			"validators": []any{
				map[string]any{
					"address": pubKey.Address().String(),
					"pub_key": map[string]any{
						"type":  "tendermint/PubKeyEd25519",
						"value": base64.StdEncoding.EncodeToString(pubKey.Bytes()),
					},
					"power": "1",
					"name":  "",
				},
			},
			"app_hash":  "",
			"app_state": map[string]any{},
		}
		data, _ := json.MarshalIndent(genesis, "", "  ")
		if err := os.WriteFile(genesisPath, data, 0644); err != nil {
			return err
		}
	}

	// Write priv_validator_state.json if missing.
	stateJSON := filepath.Join(cometHome, "data", "priv_validator_state.json")
	if !fileExists(stateJSON) {
		_ = os.WriteFile(stateJSON, []byte(`{"height":"0","round":0,"step":0}`), 0644)
	}

	return nil
}

// applyFastSync writes app_state.json so CometBFT's Info handshake returns
// the given height, causing blocksync to start from height+1 instead of genesis.
// Geth must already have the EVM state at this height (via snapshot import).
func applyFastSync(cometHome string, height int64, hashHex string) error {
	if len(hashHex) >= 2 && hashHex[:2] == "0x" {
		hashHex = hashHex[2:]
	}
	hashBytes := make([]byte, 32)
	if len(hashHex) == 64 {
		for i := 0; i < 32; i++ {
			hi := hexNibble(hashHex[i*2])
			lo := hexNibble(hashHex[i*2+1])
			hashBytes[i] = (hi << 4) | lo
		}
	}
	type appState struct {
		LastHeight           int64             `json:"last_height"`
		LastExecHash         []byte            `json:"last_exec_hash"`
		LastPayloadTime      uint64            `json:"last_payload_time"`
		ValidatorPubKeys     map[string][]byte `json:"validator_pub_keys"`
		ValidatorRewardAddrs map[string]string `json:"validator_reward_addrs"`
	}
	state := appState{
		LastHeight:           height,
		LastExecHash:         hashBytes,
		ValidatorPubKeys:     make(map[string][]byte),
		ValidatorRewardAddrs: make(map[string]string),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(cometHome, "app_state.json.tmp")
	finalPath := filepath.Join(cometHome, "app_state.json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

// filterSelf removes from a comma-separated peers string any entry whose node ID
// matches ownID. The genesis node's own address appears in DNS, so without this
// it wastes its PersistentPeers slot on itself and never dials real peers.
func filterSelf(peers, ownID string) string {
	var keep []string
	for _, p := range strings.Split(peers, ",") {
		if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, ownID+"@") {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ",")
}

// savePeersPeriodically saves connected CometBFT peers to last_peers.json every
// 2 minutes. On the next startup, loadLastPeers restores them as PersistentPeers
// so CometBFT dials them before the consensus-vs-blocksync decision is made.
func savePeersPeriodically(ctx context.Context, dataDir string) {
	save := func() {
		type peerInfo struct {
			NodeInfo struct {
				ID         string `json:"id"`
				ListenAddr string `json:"listen_addr"`
			} `json:"node_info"`
			RemoteIP string `json:"remote_ip"`
		}
		var result struct {
			Result struct{ Peers []peerInfo } `json:"result"`
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://localhost:26657/net_info")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if json.NewDecoder(resp.Body).Decode(&result) != nil {
			return
		}
		var addrs []string
		for _, p := range result.Result.Peers {
			id := p.NodeInfo.ID
			if id == "" {
				continue
			}
			port := "26656"
			host := p.RemoteIP
			la := strings.TrimPrefix(p.NodeInfo.ListenAddr, "tcp://")
			if h, pt, splitErr := net.SplitHostPort(la); splitErr == nil {
				if pt != "" {
					port = pt
				}
				if h != "" && h != "0.0.0.0" && h != "::" {
					host = h
				}
			}
			if host != "" {
				addrs = append(addrs, fmt.Sprintf("%s@%s:%s", id, host, port))
			}
		}
		if len(addrs) == 0 {
			return // don't overwrite a good list with an empty one
		}
		data, _ := json.Marshal(addrs)
		_ = os.WriteFile(filepath.Join(dataDir, "last_peers.json"), data, 0644)
	}

	// Initial save after peers have had time to connect.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	save()

	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			save() // final save on clean shutdown
			return
		case <-tick.C:
			save()
		}
	}
}

// loadLastPeers reads peers saved by a previous run. Returns nil if no file exists.
func loadLastPeers(dataDir string) []string {
	data, err := os.ReadFile(filepath.Join(dataDir, "last_peers.json"))
	if err != nil {
		return nil
	}
	var peers []string
	_ = json.Unmarshal(data, &peers)
	return peers
}

// hasLevelDBData reports whether a LevelDB directory contains any data files
// (.ldb or .sst). An empty LevelDB directory has only CURRENT, MANIFEST, and
// log files but no .ldb/.sst files.
func hasLevelDBData(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".ldb") || strings.HasSuffix(name, ".sst") {
			return true
		}
	}
	return false
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func defaultConsensusParams() map[string]any {
	return map[string]any{
		"block": map[string]any{
			"max_bytes": "4194304",
			"max_gas":   "-1",
		},
		"evidence": map[string]any{
			"max_age_num_blocks": "100000",
			"max_age_duration":   "172800000000000",
		},
		"validator": map[string]any{
			"pub_key_types": []string{"ed25519"},
		},
		"version": map[string]any{
			"app": "3",
		},
	}
}

// repairBlockstoreIfNeeded detects and auto-repairs the failure where
// CometBFT's blockstore.db loses its "blockStore" metadata key (making
// h.store.Height() return 0). Without this key CometBFT's handshake calls
// assertAppHashEqualsOneFromState immediately with no replay, which panics
// whenever app_state's height does not match state.db's AppHash — even when
// all underlying data is intact.
//
// Repair is only applied when:
//   - The "blockStore" key is missing or records height 0
//   - app_state.json reports a positive last_height
//   - geth is at that height (confirms geth data is intact)
//
// Writing {base:1, height:N} causes CometBFT to enter consensus mode
// directly instead of blocksync, so geth does not need to replay old blocks.
func repairBlockstoreIfNeeded(cometHome string) {
	blockstoreDir := filepath.Join(cometHome, "data", "blockstore.db")

	// Read app_state.json for the expected height.
	var appState struct {
		LastHeight int64 `json:"last_height"`
	}
	raw, err := os.ReadFile(filepath.Join(cometHome, "app_state.json"))
	if err != nil || json.Unmarshal(raw, &appState) != nil || appState.LastHeight <= 0 {
		return
	}

	db, err := leveldb.OpenFile(blockstoreDir, nil)
	if err != nil {
		return
	}
	defer db.Close()

	existing, _ := db.Get([]byte("blockStore"), nil)
	if existing != nil && bsDecodeHeight(existing) > 0 {
		return // key present and valid — nothing to do
	}

	// Key missing or records height 0. Only repair if geth matches app_state
	// (guards against the case where geth was wiped and needs replay instead).
	gethH, err := gethBlockNumber()
	if err != nil || int64(gethH) < appState.LastHeight-1 {
		return
	}

	val := bsEncodeVarintField(nil, 1, 1)                    // base = 1
	val = bsEncodeVarintField(val, 2, appState.LastHeight)   // height = N
	if err := db.Put([]byte("blockStore"), val, nil); err != nil {
		fmt.Printf("  Warning: blockstore auto-repair failed: %v\n", err)
		return
	}
	fmt.Printf("  Auto-repaired blockstore metadata (height %d)\n", appState.LastHeight)
}

// bsDecodeHeight extracts the height field (field 2) from a protobuf-encoded
// cmtstore.BlockStoreState. Returns 0 if the field is absent or unreadable.
func bsDecodeHeight(b []byte) int64 {
	for len(b) > 0 {
		tag, n := bsConsumeUvarint(b)
		if n <= 0 {
			return 0
		}
		b = b[n:]
		if tag&0x7 != 0 { // only handle varint wire type
			return 0
		}
		val, n := bsConsumeUvarint(b)
		if n <= 0 {
			return 0
		}
		b = b[n:]
		if tag>>3 == 2 { // field 2 = height
			return int64(val)
		}
	}
	return 0
}

func bsEncodeVarintField(dst []byte, fieldNum int, v int64) []byte {
	tag := uint64(fieldNum) << 3 // wire type 0 (varint)
	dst = bsAppendUvarint(dst, tag)
	dst = bsAppendUvarint(dst, uint64(v))
	return dst
}

func bsAppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func bsConsumeUvarint(b []byte) (uint64, int) {
	var v uint64
	for i, byt := range b {
		v |= uint64(byt&0x7F) << (7 * uint(i))
		if byt&0x80 == 0 {
			return v, i + 1
		}
		if i >= 9 {
			return 0, -1
		}
	}
	return 0, -1
}

// appStateLastHeight reads last_height from app_state.json without importing
// the full abci package. Returns 0 if the file is absent or unreadable.
func appStateLastHeight(cometHome string) int64 {
	var s struct {
		LastHeight int64 `json:"last_height"`
	}
	raw, err := os.ReadFile(filepath.Join(cometHome, "app_state.json"))
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return 0
	}
	return s.LastHeight
}

// resetValidatorSignState writes a fresh priv_validator_state.json locked to
// lastCommittedHeight with step=0. This lets the validator sign freely for the
// next height while still refusing to sign for any already-committed height.
// Called on every startup after the WAL wipe — the two must stay in sync.
func resetValidatorSignState(stateFile string, lastCommittedHeight int64) {
	type signState struct {
		Height string `json:"height"`
		Round  int    `json:"round"`
		Step   int    `json:"step"`
	}
	data, err := json.MarshalIndent(signState{Height: fmt.Sprintf("%d", lastCommittedHeight)}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(stateFile, data, 0600)
}
