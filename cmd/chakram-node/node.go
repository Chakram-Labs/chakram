package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
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
	cmtCfg.Mempool.Type = cmtconfig.MempoolTypeNop
	cmtCfg.P2P.ListenAddress = "tcp://0.0.0.0:26656"
	cmtCfg.P2P.AllowDuplicateIP = true
	cmtCfg.RPC.ListenAddress = "tcp://0.0.0.0:26657"
	if cfg.peers != "" {
		// Use discovered peers as both seeds (for PEX address discovery) and
		// persistent peers (so blocksync can download blocks immediately).
		// PEX will grow the address book over time; on future restarts the
		// node reconnects from addrbook.json without needing seeds at all.
		cmtCfg.P2P.Seeds = cfg.peers
		cmtCfg.P2P.PersistentPeers = cfg.peers
	}
	cmtCfg.Consensus.TimeoutPropose = 2 * time.Second
	cmtCfg.Consensus.TimeoutPrevote = 1 * time.Second
	cmtCfg.Consensus.TimeoutPrecommit = 1 * time.Second
	cmtCfg.Consensus.TimeoutCommit = 500 * time.Millisecond
	cmtCfg.Consensus.CreateEmptyBlocks = true

	// Fast-sync: if the user provided a trusted height+hash (from a pre-imported
	// geth snapshot), write app_state.json so Info returns that height and
	// CometBFT only blocksyncs from there to tip instead of from genesis.
	if cfg.fastSyncHeight > 0 && cfg.fastSyncHash != "" {
		if err := applyFastSync(cometHome, cfg.fastSyncHeight, cfg.fastSyncHash); err != nil {
			return fmt.Errorf("fast-sync: write app state: %w", err)
		}
		logger.Info("fast-sync: app_state set", "height", cfg.fastSyncHeight)
	}

	// CometBFT native state sync (requires external RPC servers for trust verification).
	if cfg.stateSyncEnable && cfg.stateSyncRPCServers != "" {
		cmtCfg.StateSync.Enable = true
		cmtCfg.StateSync.RPCServers = strings.Split(cfg.stateSyncRPCServers, ",")
		cmtCfg.StateSync.TrustHeight = cfg.stateSyncTrustHeight
		cmtCfg.StateSync.TrustHash = cfg.stateSyncTrustHash
		cmtCfg.StateSync.TrustPeriod = 168 * time.Hour // 1 week
	}

	cmtconfig.EnsureRoot(cometHome)

	keyFile := cmtCfg.PrivValidatorKeyFile()
	stateFile := cmtCfg.PrivValidatorStateFile()
	var pv *privval.FilePV
	if fileExists(keyFile) {
		pv = privval.LoadFilePV(keyFile, stateFile)
	} else {
		pv = privval.GenFilePV(keyFile, stateFile)
		pv.Save()
	}

	nodeKey, err := p2p.LoadOrGenNodeKey(cmtCfg.NodeKeyFile())
	if err != nil {
		return fmt.Errorf("load node key: %w", err)
	}

	cmtNode, err := node.NewNode(
		cmtCfg,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(baseApp),
		node.DefaultGenesisDocProviderFunc(cmtCfg),
		cmtconfig.DefaultDBProvider,
		node.DefaultMetricsProvider(cmtCfg.Instrumentation),
		cmtlog.NewFilter(cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stderr)), cmtlog.AllowInfo()),
	)
	if err != nil {
		return fmt.Errorf("create CometBFT node: %w", err)
	}

	if err := cmtNode.Start(); err != nil {
		return fmt.Errorf("start CometBFT node: %w", err)
	}
	logger.Info("chakram consensus started", "peers", cfg.peers)

	apiCfg := chakramapi.Config{
		ListenAddr:          "0.0.0.0:8080",
		CometRPCURL:         "http://127.0.0.1:26657",
		GethRPCURL:          "http://127.0.0.1:8545",
		HomeDir:             cometHome,
		StaticDir:           cfg.staticDir,
		StaticFS:            webUI(),
		StakingContractAddr: cfg.stakingContract,
		NodeKeyPath:         cfg.nodeKeyPath,
		P2PHost:             cfg.p2pHost,
	}
	apiSrv := chakramapi.New(apiCfg, logger)
	if err := apiSrv.Start(); err != nil {
		logger.Warn("node API unavailable", "err", err)
	}

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
	stateSyncEnable     bool
	stateSyncRPCServers string // comma-separated comet RPC URLs
	stateSyncTrustHash  string // 0x-prefixed app_hash
	stateSyncTrustHeight int64
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
	if len(hashHex) != 64 {
		return fmt.Errorf("invalid hash %q: expected 64 hex chars, got %d", hashHex, len(hashHex))
	}
	hashBytes := make([]byte, 32)
	for i := 0; i < 32; i++ {
		hi := hexNibble(hashHex[i*2])
		lo := hexNibble(hashHex[i*2+1])
		hashBytes[i] = (hi << 4) | lo
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
