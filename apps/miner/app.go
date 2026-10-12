package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"miner/appconfig"
	"miner/node"
	"miner/process"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppStatus is the single object the frontend polls / receives via event.
type AppStatus struct {
	ProcessState    string  `json:"processState"`
	PlainStatus     string  `json:"plainStatus"`
	ConsensusHeight uint64  `json:"consensusHeight"`
	GethHeight      uint64  `json:"gethHeight"`
	GethPeers       int     `json:"gethPeers"`
	ConsensusPeers  int     `json:"consensusPeers"`
	Hashrate        float64 `json:"hashrate"`
	MinerAddress    string  `json:"minerAddress"`
	BalanceCHK      string  `json:"balanceCHK"`
	BlocksFound     int     `json:"blocksFound"`
	SyncPct         float64 `json:"syncPct"`
	IsSynced        bool    `json:"isSynced"`
	IsMining        bool    `json:"isMining"`
	LightMode       bool    `json:"lightMode"`
	Threads         int     `json:"threads"`
	NetworkHashrate float64 `json:"networkHashrate"`
	UpdateAvailable string  `json:"updateAvailable"`
	Version         string  `json:"version"`
	IsConfigured    bool    `json:"isConfigured"`
}

type App struct {
	ctx  context.Context
	pm   *process.Manager
	cfg  *appconfig.Config
	geth *node.GethClient
	chk  *node.ChakramClient

	mu             sync.RWMutex
	hashrate       float64
	blocksFound    int
	updateTag      string
	recentBlocks   []node.GethBlock
	consensusPeers int
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	cfg, _ := appconfig.Load()
	a.cfg = cfg
	a.geth = node.NewGethClient("http://127.0.0.1:8545")
	a.chk = node.NewChakramClient("http://127.0.0.1:9101")

	if cfg.IsConfigured() {
		log.Println("[app] startup: config OK, starting manager")
		a.initManager()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[app] PANIC in auto-start goroutine: %v", r)
				}
			}()
			log.Println("[app] auto-start: calling pm.Start()")
			if err := a.pm.Start(); err != nil {
				log.Printf("[app] auto-start: pm.Start() returned error: %v", err)
			} else {
				log.Println("[app] auto-start: pm.Start() completed OK")
			}
		}()
	} else {
		log.Printf("[app] startup: not configured (network=%q address=%q)", cfg.Network, cfg.MinerAddress)
	}

	go a.statusLoop()
	go a.updateCheckLoop()
	go a.peerPingLoop()
	go a.minedBlocksLoop()
}

func (a *App) shutdown(_ context.Context) {
	if a.pm != nil {
		a.pm.Stop()
	}
}

func (a *App) initManager() {
	dataDir := a.effectiveDataDir()
	a.pm = process.New(process.Config{
		Network:      a.cfg.Network,
		MinerAddress: a.cfg.MinerAddress,
		Threads:      a.cfg.Threads,
		LightMode:    a.cfg.LightMode,
		DataDir:      dataDir,
	}, dataDir)
	a.pm.OnHashrate = func(v float64) {
		a.mu.Lock()
		a.hashrate = v
		a.mu.Unlock()
	}
	a.pm.OnLogLine = func(source, line string) {
		wailsRuntime.EventsEmit(a.ctx, "log:"+source, line)
	}
	a.pm.OnStateChange = func(s process.State, _ string) {
		wailsRuntime.EventsEmit(a.ctx, "stateChange", string(s))
	}
}

func (a *App) effectiveDataDir() string {
	dir, err := a.cfg.EffectiveDataDir()
	if err != nil {
		dir, _ = appconfig.DefaultDataDir()
	}
	return dir
}

func (a *App) statusLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			status := a.GetStatus()
			wailsRuntime.EventsEmit(a.ctx, "status", status)
		}
	}
}

func (a *App) updateCheckLoop() {
	check := func() {
		tag := appconfig.CheckForUpdate()
		a.mu.Lock()
		a.updateTag = tag
		a.mu.Unlock()
	}
	check()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func (a *App) peerPingLoop() {
	localChakram := node.NewChakramClient("http://127.0.0.1:9101")

	// getPeerURLs queries local chakram's /peers for a dynamic list.
	// Falls back to FallbackSeedURLs when chakram is not yet running.
	getPeerURLs := func() []string {
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
		defer cancel()
		peers, err := localChakram.Peers(ctx)
		if err == nil && len(peers) > 0 {
			return peers
		}
		return process.FallbackSeedURLs
	}

	check := func() {
		urls := getPeerURLs()
		count := 0
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, u := range urls {
			u := u
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
				defer cancel()
				if _, err := node.NewChakramClient(u).Status(ctx); err == nil {
					mu.Lock()
					count++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		a.mu.Lock()
		a.consensusPeers = count
		a.mu.Unlock()
	}
	check()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// minedBlocksLoop counts blocks mined by the configured address.
// Primary: chakram /minedblocks endpoint (efficient server-side cache).
// Fallback: incremental geth block scan (works even with old chakram binary).
func (a *App) minedBlocksLoop() {
	var gethScanned uint64
	var gethCount uint64

	check := func() {
		addr := a.cfg.MinerAddress
		if addr == "" {
			return
		}

		// Primary: chakram /minedblocks
		{
			ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
			count, err := a.chk.MinedBlockCount(ctx, addr)
			cancel()
			if err == nil && count > 0 {
				a.mu.Lock()
				a.blocksFound = int(count)
				a.mu.Unlock()
				return
			}
		}

		// Fallback: scan geth blocks incrementally from last scanned height.
		{
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			tip, err := a.geth.BlockNumber(ctx)
			if err != nil {
				return
			}
			for h := gethScanned + 1; h <= tip; h++ {
				blk, err := a.geth.GetBlockByNumber(ctx, h)
				if err != nil {
					break
				}
				if strings.EqualFold(blk.Miner, addr) {
					gethCount++
				}
				gethScanned = h
			}
			if gethCount > 0 {
				a.mu.Lock()
				a.blocksFound = int(gethCount)
				a.mu.Unlock()
			}
		}
	}
	check()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// GetStatus returns the full app state.
func (a *App) GetStatus() AppStatus {
	a.mu.RLock()
	hr := a.hashrate
	found := a.blocksFound
	updateTag := a.updateTag
	cPeers := a.consensusPeers
	a.mu.RUnlock()

	var state process.State = process.StateIdle
	var errMsg string
	if a.pm != nil {
		state = a.pm.State()
		errMsg = a.pm.ErrMsg()
	}

	isRunning := state == process.StateRunning
	status := AppStatus{
		ProcessState:    string(state),
		Hashrate:        hr,
		BlocksFound:     found,
		UpdateAvailable: updateTag,
		Version:         appconfig.CurrentVersion,
		IsConfigured:    a.cfg.IsConfigured(),
		MinerAddress:    a.cfg.MinerAddress,
		IsMining:        isRunning && a.cfg.MinerAddress != "",
		LightMode:       a.cfg.LightMode,
		Threads:         a.cfg.Threads,
		ConsensusPeers:  cPeers,
	}

	if isRunning {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()

		if height, err := a.geth.BlockNumber(ctx); err == nil {
			status.GethHeight = height
		}
		if synced, cur, high, err := a.geth.SyncStatus(ctx); err == nil {
			status.IsSynced = synced
			if synced || high == 0 {
				status.SyncPct = 100
			} else {
				status.SyncPct = float64(cur) / float64(high) * 100
			}
		}
		if peers, err := a.geth.PeerCount(ctx); err == nil {
			status.GethPeers = peers
		}
		if chkStatus, err := a.chk.Status(ctx); err == nil {
			status.ConsensusHeight = chkStatus.Height
			// Network hashrate from chakram difficulty (geth blocks have difficulty=0 post-merge).
			if chkStatus.AvgBlockTimeSecs > 0 && chkStatus.Difficulty > 0 {
				status.NetworkHashrate = float64(chkStatus.Difficulty) / chkStatus.AvgBlockTimeSecs
			}
		}
		if a.cfg.MinerAddress != "" {
			if bal, err := a.geth.GetBalance(ctx, a.cfg.MinerAddress); err == nil {
				status.BalanceCHK = weiToCHK(bal)
			}
		}
		if blocks, err := a.geth.GetLatestBlocks(ctx, 20); err == nil {
			a.mu.Lock()
			a.recentBlocks = blocks
			a.mu.Unlock()
		}

		if !status.IsSynced && status.SyncPct < 99.9 {
			if status.GethPeers == 0 && status.GethHeight == 0 {
				status.PlainStatus = "No peers — check your internet connection"
			} else {
				status.PlainStatus = fmt.Sprintf("Syncing — %.1f%% (%d blocks)", status.SyncPct, status.GethHeight)
			}
		} else {
			status.PlainStatus = "Synced"
		}
		if status.LightMode {
			status.PlainStatus += "  ·  light mode"
		}
	} else {
		status.PlainStatus = process.PlainStatus(state, errMsg)
	}

	return status
}

// GetConfig returns the current config.
func (a *App) GetConfig() appconfig.Config {
	return *a.cfg
}

// SaveConfig persists the config.
func (a *App) SaveConfig(cfg appconfig.Config) error {
	if cfg.MinerAddress != "" && !validateAddress(cfg.MinerAddress) {
		return fmt.Errorf("invalid Ethereum address")
	}
	if cfg.Threads <= 0 {
		cfg.Threads = 1
	}
	wasRunning := a.pm != nil && a.pm.State() == process.StateRunning
	*a.cfg = cfg
	if err := a.cfg.Save(); err != nil {
		return err
	}
	if wasRunning {
		a.pm.Stop()
		a.initManager()
		go a.pm.Start() //nolint:errcheck
	} else if a.pm == nil && cfg.IsConfigured() {
		a.initManager()
	}
	return nil
}

// StartMining starts geth + chakram.
func (a *App) StartMining() error {
	log.Println("[app] StartMining called")
	if !a.cfg.IsConfigured() {
		log.Println("[app] StartMining: not configured")
		return fmt.Errorf("not configured")
	}
	if a.pm == nil {
		log.Println("[app] StartMining: pm nil, calling initManager")
		a.initManager()
	}
	log.Printf("[app] StartMining: current state=%s, launching goroutine", a.pm.State())
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[app] PANIC in StartMining goroutine: %v", r)
			}
		}()
		log.Println("[app] StartMining goroutine: calling pm.Start()")
		if err := a.pm.Start(); err != nil {
			log.Printf("[app] StartMining goroutine: pm.Start() error: %v", err)
		} else {
			log.Println("[app] StartMining goroutine: pm.Start() OK")
		}
	}()
	return nil
}

// StopMining stops both processes.
func (a *App) StopMining() {
	log.Println("[app] StopMining called")
	if a.pm != nil {
		log.Printf("[app] StopMining: state=%s, calling pm.Stop()", a.pm.State())
		a.pm.Stop()
		log.Println("[app] StopMining: pm.Stop() returned")
	} else {
		log.Println("[app] StopMining: pm is nil")
	}
}

// SetThreads updates thread count.
func (a *App) SetThreads(n int) error {
	if n < 1 {
		n = 1
	}
	a.cfg.Threads = n
	return a.cfg.Save()
}

// SetLightMode updates light mode flag.
func (a *App) SetLightMode(v bool) error {
	a.cfg.LightMode = v
	return a.cfg.Save()
}

// GetLogs returns the last n log lines for the given source.
func (a *App) GetLogs(source string, n int) []string {
	if a.pm == nil {
		return nil
	}
	switch source {
	case "geth":
		return a.pm.GethLogs(n)
	case "chakram":
		return a.pm.ChkLogs(n)
	}
	return nil
}

// OpenDataFolder opens the data directory in the OS file manager.
func (a *App) OpenDataFolder() {
	openFolder(a.effectiveDataDir())
}

// OpenLogsFolder opens the logs sub-directory.
func (a *App) OpenLogsFolder() {
	dir := filepath.Join(a.effectiveDataDir(), "logs")
	os.MkdirAll(dir, 0755) //nolint:errcheck
	openFolder(dir)
}

// ResetData wipes chain data (pass "RESET" as confirmation).
func (a *App) ResetData(confirm string) error {
	if confirm != "RESET" {
		return fmt.Errorf("must pass RESET as confirmation")
	}
	if a.pm != nil {
		a.pm.Stop()
	}
	dir := a.effectiveDataDir()
	for _, sub := range []string{"geth", "consensus", "logs"} {
		os.RemoveAll(filepath.Join(dir, sub)) //nolint:errcheck
	}
	return nil
}

// ValidateAddress returns true if addr is a valid Ethereum address.
func (a *App) ValidateAddress(addr string) bool {
	return validateAddress(addr)
}

// DetectLightMode returns (recommend, reason).
func (a *App) DetectLightMode() (bool, string) {
	mem := sysTotalMemory()
	if mem > 0 && mem < 4*1024*1024*1024 {
		return true, fmt.Sprintf("Your system has %.1f GB RAM — light mode recommended to save 2 GB",
			float64(mem)/(1024*1024*1024))
	}
	return false, "Full mode available"
}

// GetMaxThreads returns runtime.NumCPU().
func (a *App) GetMaxThreads() int {
	return runtime.NumCPU()
}

// GetRecentBlocks returns the cached recent blocks for the frontend table.
func (a *App) GetRecentBlocks() []node.GethBlock {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]node.GethBlock, len(a.recentBlocks))
	copy(out, a.recentBlocks)
	return out
}

// ── helpers ──────────────────────────────────────────────────────────────────

func weiToCHK(wei *big.Int) string {
	if wei == nil {
		return "0.000000"
	}
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	whole := new(big.Int).Div(wei, divisor)
	remainder := new(big.Int).Mod(wei, divisor)
	frac := new(big.Int).Mul(remainder, big.NewInt(1_000_000))
	frac.Div(frac, divisor)
	return fmt.Sprintf("%d.%06d", whole, frac)
}


func openFolder(dir string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", dir).Start() //nolint:errcheck
	case "windows":
		exec.Command("explorer", dir).Start() //nolint:errcheck
	default:
		exec.Command("xdg-open", dir).Start() //nolint:errcheck
	}
}
