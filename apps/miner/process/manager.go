package process

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// chakramSeedsDomain is the DNS name queried for bootstrap chakram peer URLs.
// TXT records contain space-separated http:// URLs.
// Updating DNS is all that's needed to add/change seed nodes — no binary release.
const chakramSeedsDomain = "seeds.chakram.one"

// gethBootnodesDomain is the DNS name queried for geth bootnode enode:// URLs.
const gethBootnodesDomain = "bootnodes.chakram.one"

// FallbackSeedURLs are used if DNS resolution of chakramSeedsDomain fails.
// These match the three current testnet seed nodes and act as last-resort bootstrap.
var FallbackSeedURLs = []string{
	"http://34.14.218.57:9101",
	"http://207.175.24.63:9101",
	"http://136.80.159.167:9101",
}

// fallbackBootnodes are used if DNS resolution of gethBootnodesDomain fails.
// The enode public keys are stable across IP changes; only update when a seed key rotates.
var fallbackBootnodes = []string{
	"enode://0934c387bdc41c2a4fb9c2de4ddbe4003fdb7d0835bc13cebdaf50ae7cc9c0162f842bac516073a3e6c30213e03f1ff08d29f195a78608449447a058ef1d95ad@34.14.218.57:30303",
	"enode://2c111baf1cc3b3f070cc1f81c407d2f06081bbd96ff74a68beed31382c396db39929e16133f75b8084b7eb87745d0800ad35b053eebd3a5a59360ff5fb31cb47@207.175.24.63:30303",
	"enode://d066a0b05c0c591b7313a1c7c97171b793928bee24f12ab8cb90fa9e15ede96a1fa83689292b968d8dd5ae97fee885c7d6c924291b9c44ecb4cea67aa91274d5@136.80.159.167:30303",
}

// resolveSeeds resolves peer URLs for the chakram consensus layer.
// Resolution order:
//  1. TXT records on txtDomain (e.g. "seeds.chakram.one") — primary
//  2. A records on aDomain (e.g. "seed.chakram.one") built into http://IP:port — secondary
//  3. hardcoded fallback slice — last resort
func resolveSeeds(txtDomain, aDomain string, port int, fallback []string) []string {
	txts, err := net.LookupTXT(txtDomain)
	if err == nil {
		var urls []string
		for _, txt := range txts {
			for _, u := range strings.Fields(txt) {
				u = strings.TrimSpace(u)
				if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					urls = append(urls, u)
				}
			}
		}
		if len(urls) > 0 {
			log.Printf("[manager] DNS TXT %s: resolved %d peer(s)", txtDomain, len(urls))
			return urls
		}
		log.Printf("[manager] DNS TXT %s: no peer URLs in records", txtDomain)
	} else {
		log.Printf("[manager] DNS TXT %s: %v", txtDomain, err)
	}

	// A-record fallback: build http://IP:port from A records on aDomain.
	if aDomain != "" {
		if addrs, err := net.LookupHost(aDomain); err == nil && len(addrs) > 0 {
			urls := make([]string, len(addrs))
			for i, addr := range addrs {
				urls[i] = fmt.Sprintf("http://%s:%d", addr, port)
			}
			log.Printf("[manager] DNS A %s: resolved %d peer(s)", aDomain, len(urls))
			return urls
		}
	}

	log.Printf("[manager] DNS resolution failed — using hardcoded fallback peers")
	return fallback
}

// resolveBootnodes resolves geth enode URLs from a DNS TXT record, falling back
// to the provided list if DNS fails or returns no enode entries.
func resolveBootnodes(domain string, fallback []string) []string {
	txts, err := net.LookupTXT(domain)
	if err != nil {
		log.Printf("[manager] DNS bootnodes %s failed (%v) — using fallback enodes", domain, err)
		return fallback
	}
	var enodes []string
	for _, txt := range txts {
		for _, u := range strings.Fields(txt) {
			u = strings.TrimSpace(u)
			if strings.HasPrefix(u, "enode://") {
				enodes = append(enodes, u)
			}
		}
	}
	if len(enodes) == 0 {
		log.Printf("[manager] DNS bootnodes %s: no enodes in TXT records — using fallback enodes", domain)
		return fallback
	}
	log.Printf("[manager] DNS bootnodes %s: resolved %d bootnode(s)", domain, len(enodes))
	return enodes
}

type State string

const (
	StateIdle             State = "Idle"
	StateStartingGeth     State = "StartingGeth"
	StateWaitingGeth      State = "WaitingGeth"
	StateStartingChakram  State = "StartingChakram"
	StateWaitingChakram   State = "WaitingChakram"
	StateRunning          State = "Running"
	StateShuttingDown     State = "ShuttingDown"
	StateErrored          State = "Errored"
)

func PlainStatus(s State, extra string) string {
	switch s {
	case StateIdle:
		return "Ready"
	case StateStartingGeth:
		return "Starting geth…"
	case StateWaitingGeth:
		return "Waiting for geth to start…"
	case StateStartingChakram:
		return "Starting Chakram node…"
	case StateWaitingChakram:
		return "Connecting to Chakram node…"
	case StateRunning:
		if extra != "" {
			return extra
		}
		return "Running"
	case StateShuttingDown:
		return "Shutting down…"
	case StateErrored:
		if extra != "" {
			return "Error: " + extra
		}
		return "Error"
	}
	return string(s)
}

type Config struct {
	Network      string
	MinerAddress string
	Threads      int
	LightMode    bool
	DataDir      string
}

type Manager struct {
	cfg     Config
	dataDir string

	stateMu sync.RWMutex
	state   State
	errMsg  string

	gethProc *os.Process
	chkProc  *os.Process
	gethLog  *LogBuf
	chkLog   *LogBuf

	stopCh chan struct{}
	mu     sync.Mutex

	OnStateChange func(State, string)
	OnLogLine     func(source, line string)
	OnHashrate    func(float64)
	OnBlockFound  func(height uint64)
}

var (
	reHashrate   = regexp.MustCompile(`hashrate=([0-9.]+)\s*H/s`)
	reBlockFound = regexp.MustCompile(`block (\d+) mined`)
)

func New(cfg Config, dataDir string) *Manager {
	m := &Manager{cfg: cfg, dataDir: dataDir, state: StateIdle}
	m.gethLog = newLogBuf(func(line string) {
		if m.OnLogLine != nil {
			m.OnLogLine("geth", line)
		}
	})
	m.chkLog = newLogBuf(func(line string) {
		if m.OnLogLine != nil {
			m.OnLogLine("chakram", line)
		}
	})
	return m
}

func (m *Manager) setState(s State, extra string) {
	m.stateMu.Lock()
	m.state = s
	m.errMsg = extra
	m.stateMu.Unlock()
	if m.OnStateChange != nil {
		m.OnStateChange(s, extra)
	}
}

func (m *Manager) State() State {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state
}

func (m *Manager) ErrMsg() string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.errMsg
}

func (m *Manager) GethLogs(n int) []string  { return m.gethLog.Last(n) }
func (m *Manager) ChkLogs(n int) []string   { return m.chkLog.Last(n) }

func (m *Manager) Start() error {
	log.Println("[manager] Start() called")
	// Hold the lock only long enough to check state and capture the stop channel.
	// Releasing it before the long waits lets Stop() proceed without blocking.
	m.mu.Lock()
	if s := m.State(); s != StateIdle && s != StateErrored {
		m.mu.Unlock()
		log.Printf("[manager] Start() rejected: already in state %s", s)
		return fmt.Errorf("already in state %s", s)
	}
	stopCh := make(chan struct{})
	m.stopCh = stopCh
	m.setState(StateStartingGeth, "") // claim the slot before releasing mutex — prevents concurrent Start()
	m.mu.Unlock()
	log.Println("[manager] Start(): lock released, beginning startup")

	// Ensure data dirs exist.
	for _, sub := range []string{"geth", "consensus", "logs"} {
		if err := os.MkdirAll(filepath.Join(m.dataDir, sub), 0755); err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("create dir: %w", err)
		}
	}

	// Discover binaries early — needed both for ownership verification and later launch.
	gethBinEarly, _ := findBinary("geth")
	chkBinEarly, _ := findBinary("chakram")
	if chkBinEarly == "" {
		chkBinEarly, _ = findBinary("chakram2")
	}
	var ownedBins []string
	if gethBinEarly != "" {
		ownedBins = append(ownedBins, gethBinEarly)
	}
	if chkBinEarly != "" {
		ownedBins = append(ownedBins, chkBinEarly)
	}

	// Build known-PID map from the previous-run pidfile.
	pf := loadPidRecord(m.dataDir)
	knownPIDs := map[int]string{}
	if pf.Geth.PID > 0 && pf.Geth.Bin != "" {
		knownPIDs[pf.Geth.PID] = pf.Geth.Bin
	}
	if pf.Chakram.PID > 0 && pf.Chakram.Bin != "" {
		knownPIDs[pf.Chakram.PID] = pf.Chakram.Bin
	}

	// Kill orphans on our ports — only processes we can verify belong to us.
	log.Println("[manager] Start(): killing orphans on 8545/8551/9101")
	if err := killOrphans([]int{8545, 8551, 9101}, knownPIDs, ownedBins); err != nil {
		m.setState(StateErrored, err.Error())
		return err
	}
	log.Println("[manager] Start(): killOrphans done")

	// Generate JWT if missing.
	jwtPath := filepath.Join(m.dataDir, "jwt.hex")
	if _, err := os.Stat(jwtPath); os.IsNotExist(err) {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("generate jwt: %w", err)
		}
		if err := os.WriteFile(jwtPath, []byte(hex.EncodeToString(buf)), 0600); err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("write jwt: %w", err)
		}
	}

	// Init geth if needed.
	gethDataDir := filepath.Join(m.dataDir, "geth")
	chaindataDir := filepath.Join(gethDataDir, "chaindata")
	if _, err := os.Stat(chaindataDir); os.IsNotExist(err) {
		genesisPath := filepath.Join(m.dataDir, "genesis.json")
		if err := writeGenesisFile(genesisPath); err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("write genesis: %w", err)
		}
		gethBin, err := findBinary("geth")
		if err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("geth not found: %w", err)
		}
		m.setState(StateStartingGeth, "")
		initCmd := exec.Command(gethBin,
			"init",
			"--state.scheme", "hash",
			"--datadir", gethDataDir,
			genesisPath,
		)
		if out, err := initCmd.CombinedOutput(); err != nil {
			m.setState(StateErrored, err.Error())
			return fmt.Errorf("geth init failed: %w\n%s", err, out)
		}
	}

	// Bail early if Stop() was called while we were setting up.
	select {
	case <-stopCh:
		m.setState(StateIdle, "")
		return nil
	default:
	}

	// Start geth.
	gethBin, err := findBinary("geth")
	if err != nil {
		log.Printf("[manager] Start(): geth binary not found: %v", err)
		m.setState(StateErrored, err.Error())
		return fmt.Errorf("geth not found: %w", err)
	}
	log.Printf("[manager] Start(): starting geth at %s", gethBin)
	m.setState(StateStartingGeth, "")
	gethCmd := m.buildGethCmd(gethBin, gethDataDir, jwtPath)
	gethLogFile := filepath.Join(m.dataDir, "logs", "geth.log")
	if err := startProcess(gethCmd, gethLogFile, m.gethLog, nil); err != nil {
		log.Printf("[manager] Start(): startProcess(geth) failed: %v", err)
		m.setState(StateErrored, err.Error())
		return err
	}
	log.Printf("[manager] Start(): geth launched pid=%d", gethCmd.Process.Pid)
	m.mu.Lock()
	m.gethProc = gethCmd.Process
	m.mu.Unlock()
	pf.Geth = pidEntry{PID: gethCmd.Process.Pid, Bin: gethBin}
	savePidRecord(m.dataDir, pf)

	// Wait for geth — stopCh makes this interruptible by Stop().
	m.setState(StateWaitingGeth, "")
	log.Println("[manager] Start(): waiting for geth HTTP on :8545")
	if err := waitForHTTP("http://127.0.0.1:8545", 30*time.Second, stopCh); err != nil {
		log.Printf("[manager] Start(): geth wait failed: %v", err)
		gracefulKill(gethCmd.Process, 5*time.Second)
		m.setState(StateErrored, "geth did not start in time")
		return errors.New("geth did not start in time")
	}
	log.Println("[manager] Start(): geth ready")

	select {
	case <-stopCh:
		gracefulKill(gethCmd.Process, 5*time.Second)
		m.setState(StateIdle, "")
		return nil
	default:
	}

	// Start chakram.
	chkBin, err := findBinary("chakram")
	if err != nil {
		chkBin, err = findBinary("chakram2")
		if err != nil {
			log.Println("[manager] Start(): chakram binary not found")
			gracefulKill(gethCmd.Process, 5*time.Second)
			m.setState(StateErrored, "chakram binary not found")
			return errors.New("chakram binary not found")
		}
	}
	log.Printf("[manager] Start(): starting chakram at %s", chkBin)
	m.setState(StateStartingChakram, "")
	chkCmd := m.buildChakramCmd(chkBin, jwtPath)
	chkLogFile := filepath.Join(m.dataDir, "logs", "chakram.log")
	if err := startProcess(chkCmd, chkLogFile, m.chkLog, m.makeChakramParser()); err != nil {
		log.Printf("[manager] Start(): startProcess(chakram) failed: %v", err)
		gracefulKill(gethCmd.Process, 5*time.Second)
		m.setState(StateErrored, err.Error())
		return err
	}
	log.Printf("[manager] Start(): chakram launched pid=%d", chkCmd.Process.Pid)
	m.mu.Lock()
	m.chkProc = chkCmd.Process
	m.mu.Unlock()
	pf.Chakram = pidEntry{PID: chkCmd.Process.Pid, Bin: chkBin}
	savePidRecord(m.dataDir, pf)

	// Wait for chakram /status — also interruptible.
	m.setState(StateWaitingChakram, "")
	log.Println("[manager] Start(): waiting for chakram HTTP on :9101")
	if err := waitForHTTP("http://127.0.0.1:9101/status", 30*time.Second, stopCh); err != nil {
		log.Printf("[manager] Start(): chakram wait failed: %v", err)
		gracefulKill(chkCmd.Process, 5*time.Second)
		gracefulKill(gethCmd.Process, 5*time.Second)
		m.setState(StateErrored, "chakram did not start in time")
		return errors.New("chakram did not start in time")
	}
	log.Println("[manager] Start(): chakram ready")

	select {
	case <-stopCh:
		gracefulKill(chkCmd.Process, 5*time.Second)
		gracefulKill(gethCmd.Process, 5*time.Second)
		m.setState(StateIdle, "")
		return nil
	default:
	}

	log.Println("[manager] Start(): state=Running, launching watchdog")
	m.setState(StateRunning, "")
	go m.watchdog()
	return nil
}

func (m *Manager) Stop() {
	log.Println("[manager] Stop() called")
	m.mu.Lock()
	if m.State() == StateIdle {
		m.mu.Unlock()
		log.Println("[manager] Stop(): already idle, nothing to do")
		return
	}
	m.setState(StateShuttingDown, "")
	if m.stopCh != nil {
		select {
		case <-m.stopCh:
		default:
			close(m.stopCh)
		}
	}
	chkProc := m.chkProc
	gethProc := m.gethProc
	m.gethProc = nil
	m.chkProc = nil
	m.mu.Unlock()

	log.Println("[manager] Stop(): killing chakram")
	gracefulKill(chkProc, 10*time.Second)
	log.Println("[manager] Stop(): killing geth")
	gracefulKill(gethProc, 15*time.Second)
	clearPidRecord(m.dataDir)
	m.setState(StateIdle, "")
	log.Println("[manager] Stop(): done, state=Idle")
}

func (m *Manager) buildGethCmd(gethBin, dataDir, jwtPath string) *exec.Cmd {
	bootnodes := resolveBootnodes(gethBootnodesDomain, fallbackBootnodes)
	return exec.Command(gethBin,
		"--datadir", dataDir,
		"--syncmode", "full",
		"--state.scheme", "hash",
		"--gcmode", "archive",
		"--cache", "256",
		"--authrpc.addr", "127.0.0.1",
		"--authrpc.port", "8551",
		"--authrpc.jwtsecret", jwtPath,
		"--http",
		"--http.addr", "127.0.0.1",
		"--http.port", "8545",
		"--http.api", "eth,net,web3",
		"--maxpeers", "25",
		"--port", "30303",
		"--bootnodes", strings.Join(bootnodes, ","),
	)
}

func (m *Manager) buildChakramCmd(chkBin, jwtPath string) *exec.Cmd {
	// Resolve seed peers: TXT → A records → hardcoded fallback.
	// Chakram will also do its own peer exchange after startup.
	seeds := resolveSeeds(chakramSeedsDomain, "seed.chakram.one", 9101, FallbackSeedURLs)
	args := []string{
		"--network", m.cfg.Network,
		"--datadir", filepath.Join(m.dataDir, "consensus"),
		"--jwt", jwtPath,
		"--authrpc", "http://127.0.0.1:8551",
		"--rpc", "http://127.0.0.1:8545",
		"--miner", m.cfg.MinerAddress,
		"--peers", strings.Join(seeds, ","),
		"--no-discover", // miner app already resolved DNS; chakram need not duplicate it
		"--listen", ":9101",
		"--threads", strconv.Itoa(m.cfg.Threads),
	}
	if m.cfg.LightMode {
		args = append(args, "--light")
	}
	return exec.Command(chkBin, args...)
}

func (m *Manager) makeChakramParser() func(string) {
	return func(line string) {
		if matches := reHashrate.FindStringSubmatch(line); len(matches) == 2 {
			if v, err := strconv.ParseFloat(matches[1], 64); err == nil {
				if m.OnHashrate != nil {
					m.OnHashrate(v)
				}
			}
		}
		if matches := reBlockFound.FindStringSubmatch(line); len(matches) == 2 {
			if v, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
				if m.OnBlockFound != nil {
					m.OnBlockFound(v)
				}
			}
		}
	}
}

func (m *Manager) watchdog() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[manager] PANIC in watchdog: %v", r)
		}
	}()
	log.Println("[manager] watchdog: started")
	backoff := 5 * time.Second
	for {
		select {
		case <-m.stopCh:
			return
		case <-time.After(5 * time.Second):
		}

		gethAlive := isAlive(m.gethProc)
		chkAlive := isAlive(m.chkProc)

		if !gethAlive {
			log.Printf("[miner] geth died — restarting both processes")
			m.setState(StateErrored, "geth exited unexpectedly")
			gracefulKill(m.chkProc, 5*time.Second)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			if err := m.restart(); err != nil {
				log.Printf("[miner] restart failed: %v", err)
			}
			return
		}
		if !chkAlive {
			log.Printf("[miner] chakram died — restarting chakram only")
			m.setState(StateStartingChakram, "")
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			chkBin, err := findBinary("chakram")
			if err != nil {
				chkBin, err = findBinary("chakram2")
				if err != nil {
					m.setState(StateErrored, "chakram binary not found")
					return
				}
			}
			jwtPath := filepath.Join(m.dataDir, "jwt.hex")
			chkCmd := m.buildChakramCmd(chkBin, jwtPath)
			chkLogFile := filepath.Join(m.dataDir, "logs", "chakram.log")
			if err := startProcess(chkCmd, chkLogFile, m.chkLog, m.makeChakramParser()); err != nil {
				m.setState(StateErrored, err.Error())
				return
			}
			m.chkProc = chkCmd.Process
			m.setState(StateRunning, "")
			backoff = 5 * time.Second
			continue
		}
		backoff = 5 * time.Second
	}
}

func (m *Manager) restart() error {
	return m.Start()
}

// startProcess launches cmd, tees its stdout+stderr to logBuf and a log file,
// and optionally calls parser on each line.
func startProcess(cmd *exec.Cmd, logFile string, buf *LogBuf, parser func(string)) error {
	setProcAttrs(cmd)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	pw.Close()

	go func() {
		defer pr.Close()
		f, _ := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		defer func() {
			if f != nil {
				f.Close()
			}
		}()
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			buf.Append(line)
			if f != nil {
				fmt.Fprintln(f, line)
			}
			if parser != nil {
				parser(line)
			}
		}
	}()
	return nil
}

func waitForHTTP(url string, timeout time.Duration, stop <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-stop:
			return errors.New("stopped")
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func isAlive(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	return signalProcess(proc)
}

func findBinary(name string) (string, error) {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return exec.LookPath(name)
}

func gracefulKill(proc *os.Process, timeout time.Duration) {
	if proc == nil {
		return
	}
	sendTermSignal(proc)
	done := make(chan struct{})
	go func() {
		proc.Wait() //nolint:errcheck
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		proc.Kill() //nolint:errcheck
	}
}

func writeGenesisFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, genesisJSON, 0644)
}

// killOrphans terminates our own orphaned processes on the given ports.
// Returns a user-friendly error if a port is held by an unrecognized program.
func killOrphans(ports []int, knownPIDs map[int]string, ownedBins []string) error {
	for _, p := range ports {
		if err := killOnPort(p, knownPIDs, ownedBins); err != nil {
			return err
		}
	}
	return nil
}

