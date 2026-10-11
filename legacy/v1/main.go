// main.go — Chakram CLI entrypoint.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"node"}
	}

	switch args[0] {
	case "node":
		runNode(args[1:])
	case "wallet":
		if len(args) < 2 {
			printUsage()
			os.Exit(1)
		}
		runWallet(args[1:])
	case "send":
		runSend(args[1:])
	case "status":
		runStatus(args[1:])
	default:
		printUsage()
		os.Exit(1)
	}
}

// ── flag parser ───────────────────────────────────────────────────────────────

// parseFlags parses --flag, --flag=value, and --flag value forms.
// Boolean flags map to "true"; value flags map to their value.
func parseFlags(args []string) map[string]string {
	flags := make(map[string]string)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		a = a[2:]
		if idx := strings.IndexByte(a, '='); idx >= 0 {
			flags[a[:idx]] = a[idx+1:]
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			flags[a] = args[i+1]
			i++
		} else {
			flags[a] = "true"
		}
	}
	return flags
}

// ── node command ──────────────────────────────────────────────────────────────

func runNode(args []string) {
	flags := parseFlags(args)

	cfg := DefaultConfig(flags["testnet"] == "true")
	cfg.Mine = flags["mine"] == "true"
	cfg.SeedMode = flags["seed-mode"] == "true"
	cfg.RPCPublic = flags["rpc-public"] == "true"
	if p := flags["password"]; p != "" {
		cfg.Password = p
	}
	if m := flags["mineraddress"]; m != "" {
		cfg.MinerAddr = m
	}
	if t := flags["threads"]; t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			cfg.MiningThreads = n
		}
	}

	node, err := NewNode(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
	if err := node.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	fmt.Println("\nShutting down Chakram node...")
	stopDone := make(chan struct{})
	go func() {
		node.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
	}
	os.Exit(0)
}

// ── wallet commands ───────────────────────────────────────────────────────────

func runWallet(args []string) {
	testnet := false
	for _, a := range args {
		if a == "--testnet" {
			testnet = true
		}
	}

	flags := parseFlags(args)

	switch args[0] {
	case "new":
		cfg := DefaultConfig(testnet)
		password := flags["password"]
		if password == "" || password == "chakram" {
			fmt.Fprintln(os.Stderr, "FATAL: --password is required and must not be 'chakram'.")
			fmt.Fprintln(os.Stderr, "  Example: chakram wallet new --password <secret>")
			os.Exit(1)
		}
		if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: create data dir: %v\n", err)
			os.Exit(1)
		}
		w, err := NewWallet()
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
			os.Exit(1)
		}
		if err := w.SaveToFile(cfg.WalletFile, password); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: save wallet: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Address:  %s\n", w.Address)
		fmt.Printf("Mnemonic: %s\n", w.Mnemonic)
		fmt.Printf("Saved to: %s\n", cfg.WalletFile)
		fmt.Println("IMPORTANT: Back up your mnemonic phrase — it cannot be recovered!")

	case "recover":
		runWalletRecover(args[1:], flags, testnet)

	case "address":
		cfg := DefaultConfig(testnet)
		password := flags["password"]
		if password == "" || password == "chakram" {
			fmt.Fprintln(os.Stderr, "FATAL: --password is required and must not be 'chakram'.")
			fmt.Fprintln(os.Stderr, "  Example: chakram wallet address --password <secret>")
			os.Exit(1)
		}
		w, err := LoadWalletFromFile(cfg.WalletFile, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(w.Address)

	case "balance":
		cfg := DefaultConfig(testnet)
		bc, err := NewBlockchain(cfg.DataDir, false, NetworkChainID(testnet)) // read-only; AddBlock never called
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
			os.Exit(1)
		}
		defer bc.Close()
		password := flags["password"]
		if password == "" || password == "chakram" {
			fmt.Fprintln(os.Stderr, "FATAL: --password is required and must not be 'chakram'.")
			fmt.Fprintln(os.Stderr, "  Example: chakram wallet balance --password <secret>")
			os.Exit(1)
		}
		w, err := LoadWalletFromFile(cfg.WalletFile, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
			os.Exit(1)
		}
		bal, err := w.GetBalance(bc.UTXOSet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%.6f CHK\n", float64(bal)/float64(CashPerCHK))

	default:
		printUsage()
		os.Exit(1)
	}
}

// ── wallet recover command ────────────────────────────────────────────────────

func runWalletRecover(args []string, flags map[string]string, testnet bool) {
	// Collect positional args (mnemonic words, or --mnemonic "word1 word2 ...").
	mnemonic := flags["mnemonic"]
	if mnemonic == "" {
		var words []string
		for _, a := range args {
			if !strings.HasPrefix(a, "--") {
				words = append(words, a)
			}
		}
		mnemonic = strings.Join(words, " ")
	}
	if mnemonic == "" {
		fmt.Fprintln(os.Stderr, "usage: chakram wallet recover --mnemonic \"word1 word2 ... word12\" [--password <pass>] [--testnet]")
		os.Exit(1)
	}

	w, err := WalletFromMnemonic(mnemonic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recover failed: %v\n", err)
		os.Exit(1)
	}

	cfg := DefaultConfig(testnet)
	password := flags["password"]
	if password == "" || password == "chakram" {
		fmt.Fprintln(os.Stderr, "FATAL: --password is required and must not be 'chakram'.")
		fmt.Fprintln(os.Stderr, "  Example: chakram wallet recover --mnemonic \"...\" --password <secret>")
		os.Exit(1)
	}

	if err := w.SaveToFile(cfg.WalletFile, password); err != nil {
		fmt.Fprintf(os.Stderr, "save wallet: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wallet recovered: %s\n", w.Address)
	fmt.Printf("Saved to:         %s\n", cfg.WalletFile)
}

// ── send command ──────────────────────────────────────────────────────────────

func runSend(args []string) {
	// Collect positional args (non-flag tokens) and parse flags.
	var positional []string
	flags := parseFlags(args)
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
		}
	}

	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: chakram send <address> <amount> [--testnet] [--password <pass>]")
		os.Exit(1)
	}

	toAddress := positional[0]
	var amountCHK float64
	if _, err := fmt.Sscanf(positional[1], "%f", &amountCHK); err != nil {
		fmt.Fprintf(os.Stderr, "invalid amount: %s\n", positional[1])
		os.Exit(1)
	}

	cfg := DefaultConfig(flags["testnet"] == "true")
	if p := flags["password"]; p != "" {
		cfg.Password = p
	}

	txid, err := SendCHK(cfg, toAddress, amountCHK)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	rpcPort := RPCPortMainnet
	if cfg.Testnet {
		rpcPort = RPCPortTestnet
	}

	fmt.Println("✓ Transaction submitted")
	fmt.Printf("  From:   %s\n", cfg.WalletFile)
	fmt.Printf("  To:     %s\n", toAddress)
	fmt.Printf("  Amount: %.6f CHK\n", amountCHK)
	fmt.Printf("  Fee:    %.6f CHK\n", float64(MinTxFee)/float64(CashPerCHK))
	fmt.Printf("  TxID:   %s\n", txid)
	fmt.Println()
	fmt.Println("Transaction will confirm in the next block (~30 seconds)")
	fmt.Printf("Check: http://localhost:%d/tx/%s\n", rpcPort, txid)
}

// ── status command ────────────────────────────────────────────────────────────

func runStatus(args []string) {
	testnet := false
	for _, a := range args {
		if a == "--testnet" {
			testnet = true
		}
	}
	cfg := DefaultConfig(testnet)
	bc, err := NewBlockchain(cfg.DataDir, false, NetworkChainID(testnet)) // read-only; AddBlock never called
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
	defer bc.Close()
	valid, err := bc.IsValid()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chain validation error: %v\n", err)
	}
	fmt.Printf("Height:      %d\n", bc.GetHeight())
	fmt.Printf("Chain valid: %v\n", valid)
}

// ── usage ─────────────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  chakram node                                     — start full node (RPC on localhost only)")
	fmt.Println("  chakram node --mine                              — start node with mining")
	fmt.Println("  chakram node --rpc-public                        — expose RPC on 0.0.0.0 (explorer/monitoring)")
	fmt.Println("  chakram node --seed-mode                         — infrastructure seed node")
	fmt.Println("  chakram node --testnet                           — start on testnet")
	fmt.Println("  chakram wallet new                               — generate new wallet")
	fmt.Println("  chakram wallet recover --mnemonic \"<12 words>\"  — restore wallet from mnemonic")
	fmt.Println("  chakram wallet address [--password <pass>]       — show wallet address")
	fmt.Println("  chakram wallet balance [--password <pass>]       — show wallet balance")
	fmt.Println("  chakram send <address> <amount>                  — send CHK")
	fmt.Println("  chakram status                                   — show chain status")
}
