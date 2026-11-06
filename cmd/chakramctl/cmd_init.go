package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	mode := fs.String("mode", "dev", "node mode: dev, localnet, fullnode")
	chainID := fs.String("chain-id", "chakram-devnet-1", "chain ID")
	genesisDir := fs.String("genesis-dir", "", "fullnode: directory containing comet-genesis.json and geth-genesis.json")
	peers := fs.String("peers", "", "fullnode: comma-separated CometBFT persistent peers (node-id@host:port,...)")
	bootnodes := fs.String("bootnodes", "", "fullnode: geth bootnode enode URLs (comma-separated)")
	_ = fs.Parse(args)

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	switch *mode {
	case "dev", "localnet", "fullnode":
	default:
		fatalf("unknown mode %q; choose dev, localnet, or fullnode", *mode)
	}

	dir := *projectDir

	// Prefer docker/.env — that's where docker compose reads variables from.
	// Fall back to cwd/.env for non-standard layouts.
	envFile := filepath.Join(dir, "docker", ".env")
	if _, err := os.Stat(envFile); err != nil {
		envFile = filepath.Join(dir, ".env")
	}

	// Create .env from .env.example if neither exists yet.
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		for _, candidate := range []string{
			filepath.Join(dir, "docker", ".env.example"),
			filepath.Join(dir, ".env.example"),
		} {
			if data, err := os.ReadFile(filepath.Clean(candidate)); err == nil {
				if err := os.WriteFile(envFile, data, 0600); err != nil {
					fatalf("write .env: %v", err)
				}
				fmt.Printf("Created .env from %s\n", candidate)
				break
			}
		}
	}

	// Update .env with mode-specific values.
	setEnv(envFile, "CHAKRAM_MODE", *mode)
	setEnv(envFile, "CHAKRAM_CHAIN_ID", *chainID)

	fmt.Printf("Initializing chakram node in %s (mode=%s chain=%s)\n", dir, *mode, *chainID)

	if *mode == "fullnode" {
		if *genesisDir == "" {
			fatalf("--genesis-dir is required for fullnode mode\n  Provide a directory containing comet-genesis.json and geth-genesis.json")
		}
		if err := initFullnode(dir, envFile, *genesisDir, *peers, *bootnodes); err != nil {
			fatalf("fullnode init: %v", err)
		}
		return
	}

	// dev/localnet: run setup.sh which generates genesis and initialises volumes.
	// Try cwd/setup.sh first, then docker/setup.sh (most common repo layout).
	setupSh := filepath.Join(dir, "setup.sh")
	if _, err := os.Stat(setupSh); err != nil {
		setupSh = filepath.Join(dir, "docker", "setup.sh")
	}
	if _, err := os.Stat(setupSh); err == nil {
		fmt.Printf("Running %s --mode %s...\n", setupSh, *mode)
		cmd := exec.Command("bash", setupSh, "--mode", *mode, "--chain-id", *chainID)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fatalf("setup.sh: %v", err)
		}
	} else {
		fmt.Printf("Note: setup.sh not found in %s or %s/docker/; keys and config will be generated on first 'up'\n", dir, dir)
	}

	fmt.Printf("\nDone. Next step:\n")
	fmt.Printf("  chakramctl up --mode %s\n", *mode)
}

// initFullnode initialises Docker volumes for a fullnode that syncs from an existing network.
func initFullnode(projectDir, envFile, genesisDir, peers, bootnodes string) error {
	cometGenesis := filepath.Join(genesisDir, "comet-genesis.json")
	gethGenesis := filepath.Join(genesisDir, "geth-genesis.json")

	for _, f := range []string{cometGenesis, gethGenesis} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("missing %s: %w", filepath.Base(f), err)
		}
	}

	// Extract chain ID from comet-genesis.json to update .env.
	var cometGen struct {
		ChainID string `json:"chain_id"`
	}
	if data, err := os.ReadFile(filepath.Clean(cometGenesis)); err == nil {
		_ = json.Unmarshal(data, &cometGen)
	}
	if cometGen.ChainID != "" {
		setEnv(envFile, "CHAKRAM_CHAIN_ID", cometGen.ChainID)
		fmt.Printf("  chain_id=%s (from genesis)\n", cometGen.ChainID)
	}

	if peers != "" {
		setEnv(envFile, "CHAKRAM_PEERS", peers)
	}
	if bootnodes != "" {
		setEnv(envFile, "CHAKRAM_GETH_BOOTNODES", bootnodes)
	}

	gethImage := "ethereum/client-go:v1.17.6"

	// Create Docker volumes.
	for _, vol := range []string{"chakram_geth-fullnode-data", "chakram_chakram-fullnode-data"} {
		if err := runCmd("docker", "volume", "create", vol); err != nil {
			return fmt.Errorf("create volume %s: %w", vol, err)
		}
	}
	fmt.Println("  Docker volumes created")

	// Init geth with geth-genesis.json — run as uid 1000 (same as runtime container).
	absGethGenesis, _ := filepath.Abs(gethGenesis)
	genesisAbsDir := filepath.Dir(absGethGenesis)
	if err := runCmd("docker", "run", "--rm",
		"-v", "chakram_geth-fullnode-data:/data",
		"alpine", "chown", "1000:1000", "/data",
	); err != nil {
		return fmt.Errorf("pre-chown geth volume: %w", err)
	}
	if err := runCmd("docker", "run", "--rm", "--user", "1000:1000",
		"-v", "chakram_geth-fullnode-data:/data",
		"-v", genesisAbsDir+":/genesis:ro",
		gethImage,
		"--datadir", "/data", "init", "/genesis/geth-genesis.json",
	); err != nil {
		return fmt.Errorf("geth init: %w", err)
	}
	fmt.Println("  geth data initialised")

	// Install CometBFT genesis + initial validator state into comet volume.
	// NOTE: volume is mounted at /home/chakram/.chakram in the container,
	// so write config/ and data/ at the volume root (not inside a .chakram/ subdirectory).
	absCometGenesis, _ := filepath.Abs(cometGenesis)
	cometAbsDir := filepath.Dir(absCometGenesis)
	if err := runCmd("docker", "run", "--rm",
		"-v", "chakram_chakram-fullnode-data:/comet",
		"-v", cometAbsDir+":/genesis:ro",
		"alpine", "sh", "-c",
		`mkdir -p /comet/config /comet/data && `+
			`cp /genesis/comet-genesis.json /comet/config/genesis.json && `+
			`printf '{"height":"0","round":0,"step":0}' > /comet/data/priv_validator_state.json && `+
			`chown -R 1000:1000 /comet`,
	); err != nil {
		return fmt.Errorf("comet volume init: %w", err)
	}
	fmt.Println("  CometBFT genesis installed")

	fmt.Printf("\nFullnode init complete.\n")
	fmt.Printf("  peers=%s\n", peers)
	fmt.Printf("\nNext step:\n")
	fmt.Printf("  chakramctl up --mode fullnode --project-dir %s\n", projectDir)
	return nil
}

// runCmd runs a command and streams its output.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func modeToProfile(mode string) string {
	switch mode {
	case "localnet":
		return "localnet"
	case "fullnode":
		return "fullnode"
	default:
		return "dev"
	}
}

// setEnv writes KEY=VALUE into envFile, creating it if needed, updating if the key exists.
func setEnv(path, key, value string) {
	data, _ := os.ReadFile(filepath.Clean(path))
	lines := splitLines(string(data))
	found := false
	for i, line := range lines {
		if len(line) > len(key)+1 && line[:len(key)] == key && line[len(key)] == '=' {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	content := joinLines(lines)
	_ = os.WriteFile(filepath.Clean(path), []byte(content), 0600)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func joinLines(lines []string) string {
	s := ""
	for i, l := range lines {
		s += l
		if i < len(lines)-1 {
			s += "\n"
		}
	}
	return s + "\n"
}

// dockerAvailable checks if docker is in PATH.
func dockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}
