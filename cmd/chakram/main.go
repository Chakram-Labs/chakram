// chakram is the Chakram consensus node.
// It runs a CometBFT in-process node that drives geth via the Engine API.
//
// Usage:
//
//	chakram init  --home <dir> [--chain-id <id>]
//	chakram start --home <dir> --engine-url <url> --jwt-path <file> --reward-address <addr> --geth-rpc <url>
package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"

	cmtconfig "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"

	chakramabci "chakram/abci"
	chakramapi "chakram/api"
	"chakram/engine"
	"chakram/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: chakram <init|start> [flags]")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "start":
		cmdStart(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("chakram %s\n", version.Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(1)
	}
}

// ── init subcommand ────────────────────────────────────────────────────────

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	home := fs.String("home", filepath.Join(os.Getenv("HOME"), ".chakram"), "CometBFT home directory")
	chainID := fs.String("chain-id", "chakram-devnet-1", "CometBFT chain ID")
	_ = fs.Parse(args)

	logger := makeLogger("info")

	cfg := cmtconfig.DefaultConfig()
	cfg.SetRoot(*home)

	// Apply consensus params from D9.
	cfg.Consensus.TimeoutPropose = 2 * time.Second
	cfg.Consensus.TimeoutPrevote = 1 * time.Second
	cfg.Consensus.TimeoutPrecommit = 1 * time.Second
	cfg.Consensus.TimeoutCommit = 500 * time.Millisecond
	cfg.Consensus.CreateEmptyBlocks = true
	cfg.Mempool.Type = cmtconfig.MempoolTypeNop
	cfg.P2P.AllowDuplicateIP = true // for single-machine devnet

	cmtconfig.EnsureRoot(*home)

	// Generate validator key if not present.
	keyFile := cfg.PrivValidatorKeyFile()
	stateFile := cfg.PrivValidatorStateFile()
	if !fileExists(keyFile) {
		pv := privval.GenFilePV(keyFile, stateFile)
		pv.Save()
		pubKey, _ := pv.GetPubKey()
		logger.Info("generated validator key", "address", pubKey.Address(), "key_file", keyFile)
	} else {
		logger.Info("validator key exists", "key_file", keyFile)
	}

	// Generate node key if not present.
	if !fileExists(cfg.NodeKeyFile()) {
		if _, err := p2p.LoadOrGenNodeKey(cfg.NodeKeyFile()); err != nil {
			logger.Error("generate node key", "err", err)
			os.Exit(1)
		}
		logger.Info("generated node key", "file", cfg.NodeKeyFile())
	}

	// Write config.toml.
	configFilePath := filepath.Join(*home, "config", "config.toml")
	cmtconfig.WriteConfigFile(configFilePath, cfg)
	logger.Info("wrote config.toml", "file", configFilePath)

	// Write genesis.json with the generated validator key.
	genesisPath := cfg.BaseConfig.GenesisFile()
	if !fileExists(genesisPath) {
		pv2 := privval.LoadFilePV(keyFile, stateFile)
		pubKey, err := pv2.GetPubKey()
		if err != nil {
			logger.Error("get pubkey for genesis", "err", err)
			os.Exit(1)
		}
		genesis := map[string]any{
			"genesis_time":     time.Now().UTC().Format(time.RFC3339),
			"chain_id":         *chainID,
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
			logger.Error("write genesis", "err", err)
			os.Exit(1)
		}
		logger.Info("wrote genesis", "file", genesisPath, "validator", pubKey.Address())
	}

	logger.Info("init complete", "home", *home)
}

func defaultConsensusParams() map[string]any {
	// CometBFT v0.40 reads genesis via protobuf JSON which encodes int64 as strings.
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

// ── start subcommand ───────────────────────────────────────────────────────

func cmdStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	home := fs.String("home", filepath.Join(os.Getenv("HOME"), ".chakram"), "CometBFT home directory")
	engineURL := fs.String("engine-url", "http://127.0.0.1:8551", "geth Engine API URL")
	jwtPath := fs.String("jwt-path", "", "path to JWT secret file (hex-encoded 32 bytes)")
	rewardAddr := fs.String("reward-address", "", "0x-prefixed EVM address to receive block rewards (auto-generated if empty)")
	gethRPC := fs.String("geth-rpc", "http://127.0.0.1:8545", "geth plain HTTP RPC URL (for health gate)")
	gethDataDir := fs.String("geth-data-dir", "", "path to geth --datadir (for snapshots)")
	gethBin := fs.String("geth-bin", "", "path to geth binary (for snapshots)")
	snapshotInterval := fs.Int64("snapshot-interval", 0, "blocks between snapshots; 0 = disabled")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	faultyRate := fs.Float64("faulty-rate", 0, "faulty proposer rate 0.0-1.0 (build tag: faulty only)")
	p2pPort := fs.Int("p2p-port", 26656, "CometBFT P2P listen port")
	rpcPort := fs.Int("rpc-port", 26657, "CometBFT RPC listen port")
	peers := fs.String("peers", "", "comma-separated persistent peers (id@host:port)")
	apiAddr := fs.String("api-addr", "", "Node API listen address, e.g. 0.0.0.0:8080 (empty = disabled)")
	apiDevMode := fs.Bool("api-dev-mode", false, "enable faucet endpoint in Node API")
	apiFaucetAddr := fs.String("api-faucet-addr", "", "faucet sender address (dev mode)")
	apiFaucetKey  := fs.String("api-faucet-key", "", "faucet sender private key hex (dev mode)")
	apiStaticDir := fs.String("api-static-dir", "", "path to dashboard static files to serve at /")
	apiLogFile := fs.String("api-log-file", "", "path to chakram log file for SSE streaming")
	stakingContract := fs.String("staking-contract", "", "0x-prefixed address of StakingRegistry.sol (empty = PoA mode)")
	apiP2PHost := fs.String("api-p2p-host", "", "publicly reachable hostname/IP for P2P (used in /api/node-info)")
	_ = fs.Parse(args)

	if *jwtPath == "" {
		fmt.Fprintln(os.Stderr, "chakram start: --jwt-path is required")
		os.Exit(1)
	}

	logger := makeLogger(*logLevel)

	resolvedRewardAddr := resolveRewardAddr(*home, *rewardAddr)
	nodeKeyPath := ""
	if kp := filepath.Join(*home, "reward-key.json"); fileExists(kp) {
		nodeKeyPath = kp
	}

	eng, err := engine.NewClientFromFile(*engineURL, *jwtPath)
	if err != nil {
		logger.Error("create engine client", "err", err)
		os.Exit(1)
	}

	baseApp, err := chakramabci.NewChakramApp(eng, chakramabci.Config{
		Home:                *home,
		RewardAddress:       resolvedRewardAddr,
		GethRPCURL:          *gethRPC,
		StakingContractAddr: *stakingContract,
		GethDataDir:         *gethDataDir,
		GethBin:             *gethBin,
		SnapshotInterval:    *snapshotInterval,
	}, logger)
	if err != nil {
		logger.Error("create ABCI app", "err", err)
		os.Exit(1)
	}

	app := maybeFaultyApp(baseApp, *faultyRate)

	cfg := cmtconfig.DefaultConfig()
	cfg.SetRoot(*home)
	cfg.Mempool.Type = cmtconfig.MempoolTypeNop

	// Load config from disk if it exists (written by init).
	if diskCfg, err := loadConfig(*home); err == nil {
		cfg = diskCfg
	}

	// Apply per-node port overrides (for multi-node devnet).
	cfg.P2P.ListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", *p2pPort)
	cfg.RPC.ListenAddress = fmt.Sprintf("tcp://0.0.0.0:%d", *rpcPort)
	cfg.P2P.AllowDuplicateIP = true // required for single-machine devnet (all nodes share 127.0.0.1)
	if *peers != "" {
		cfg.P2P.PersistentPeers = *peers
	}

	cmtconfig.EnsureRoot(*home)

	keyFile := cfg.PrivValidatorKeyFile()
	stateFile := cfg.PrivValidatorStateFile()
	var pv *privval.FilePV
	if fileExists(keyFile) {
		pv = privval.LoadFilePV(keyFile, stateFile)
	} else {
		pv = privval.GenFilePV(keyFile, stateFile)
		pv.Save()
	}

	nodeKey, err := p2p.LoadOrGenNodeKey(cfg.NodeKeyFile())
	if err != nil {
		logger.Error("load node key", "err", err)
		os.Exit(1)
	}

	cmtLogger := cmtlog.NewNopLogger()

	n, err := node.NewNode(
		cfg,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(app),
		node.DefaultGenesisDocProviderFunc(cfg),
		cmtconfig.DefaultDBProvider,
		node.DefaultMetricsProvider(cfg.Instrumentation),
		cmtLogger,
	)
	if err != nil {
		logger.Error("create CometBFT node", "err", err)
		os.Exit(1)
	}

	if err := n.Start(); err != nil {
		logger.Error("start CometBFT node", "err", err)
		os.Exit(1)
	}
	logger.Info("chakram started", "home", *home, "engine", *engineURL)

	// Start optional Node API server.
	var apiSrv *chakramapi.Server
	if *apiAddr != "" {
		cometRPCURL := fmt.Sprintf("http://127.0.0.1:%d", *rpcPort)
		apiSrv = chakramapi.New(chakramapi.Config{
			ListenAddr:          *apiAddr,
			CometRPCURL:         cometRPCURL,
			GethRPCURL:          *gethRPC,
			DevMode:             *apiDevMode,
			FaucetAddr:          *apiFaucetAddr,
			FaucetPrivKey:       *apiFaucetKey,
			LogFile:             *apiLogFile,
			StaticDir:           *apiStaticDir,
			RewardAddress:       resolvedRewardAddr,
			StakingContractAddr: *stakingContract,
			HomeDir:             *home,
			P2PHost:             *apiP2PHost,
			NodeKeyPath:         nodeKeyPath,
		}, logger)
		if err := apiSrv.Start(); err != nil {
			logger.Error("start node api", "err", err)
			// Non-fatal: the node continues without the API.
			apiSrv = nil
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down")
	if apiSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = apiSrv.Stop(ctx)
	}
	if err := n.Stop(); err != nil {
		logger.Error("stop CometBFT node", "err", err)
	}
	n.Wait()
	logger.Info("stopped")
}

func loadConfig(home string) (*cmtconfig.Config, error) {
	cfg := cmtconfig.DefaultConfig()
	cfg.SetRoot(home)
	cfgFile := filepath.Join(home, "config", "config.toml")
	if !fileExists(cfgFile) {
		return nil, fmt.Errorf("no config file at %s", cfgFile)
	}
	// Config was written by init; re-apply root so paths resolve correctly.
	cfg.Mempool.Type = cmtconfig.MempoolTypeNop
	return cfg, nil
}

func makeLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type rewardKeyFile struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

func generateRewardKey() (*rewardKeyFile, error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	uncompressed := privKey.PubKey().SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hash := h.Sum(nil)
	address := "0x" + hex.EncodeToString(hash[12:])
	return &rewardKeyFile{
		Address:    address,
		PrivateKey: hex.EncodeToString(privKey.Serialize()),
	}, nil
}

func resolveRewardAddr(home, addr string) string {
	keyPath := filepath.Join(home, "reward-key.json")
	// Key file takes priority: it carries the private key needed for staking ops.
	if fileExists(keyPath) {
		data, err := os.ReadFile(keyPath)
		if err == nil {
			var k rewardKeyFile
			if json.Unmarshal(data, &k) == nil && k.Address != "" && k.PrivateKey != "" {
				return k.Address
			}
		}
	}
	// No usable key file; fall back to explicit address without key generation.
	if addr != "" {
		return addr
	}
	// Nothing set: auto-generate a fresh key and persist it.
	k, err := generateRewardKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[chakram] warning: could not generate EVM key: %v\n", err)
		return "0x0000000000000000000000000000000000000000"
	}
	data, _ := json.MarshalIndent(k, "", "  ")
	if err := os.WriteFile(keyPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "[chakram] warning: could not save reward key: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[chakram] generated node EVM wallet: %s\n", k.Address)
		fmt.Fprintf(os.Stderr, "[chakram] key saved to: %s\n", keyPath)
	}
	return k.Address
}
