// chakram-node is a self-contained Chakram full-node binary.
// It downloads geth on first run, bootstraps from the network, then
// runs geth + the chakram consensus engine in one process.
//
// Usage:
//
//	chakram-node                        # start (auto-bootstraps on first run)
//	chakram-node start [--network URL]  # explicit start
//	chakram-node status                 # show sync status
//	chakram-node stop                   # stop the running node
//	chakram-node version                # print version
package main

import (
	"flag"
	"fmt"
	"os"

	"chakram/internal/version"
)

func main() {
	cmd := "start"
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "start", "run":
		cmdStart(args)
	case "status":
		cmdStatus(args)
	case "stop":
		cmdStop(args)
	case "snapshot":
		cmdSnapshot(args)
	case "version", "--version", "-v":
		fmt.Printf("chakram-node %s\n", version.Version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "chakram-node: unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`chakram-node — Chakram full node (no Docker required)

Usage:
  chakram-node [start] [flags]   Start the node (default command)
  chakram-node status            Show sync status
  chakram-node stop              Stop the running node
  chakram-node version           Print version

Start flags:
  --network URL    Fallback bootstrap URL (default: https://node.chakram.one)
  --seeds LIST     Comma-separated DNS seed names for peer discovery
                   (default: seeds.chakram.one)
                   Multiple orgs can publish seeds under their own domain;
                   add them here without changing code.
  --data-dir DIR   Data directory (default: ~/.chakram-node)
  --no-browser     Don't open the browser on startup
  --reward ADDR    0x-prefixed EVM address for block rewards
                   (auto-generated on first run if omitted)

On first run, chakram-node will:
  1. Discover peers via DNS (seeds.chakram.one + any --seeds you add)
  2. Download geth for your platform
  3. Fetch genesis files from a discovered bootstrap node
  4. Initialize chain data
  5. Generate validator keys
  6. Start syncing and open the status page in your browser

`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "chakram-node: "+format+"\n", args...)
	os.Exit(1)
}

func startFlags(args []string) (
	network, dataDir, reward, seeds string,
	noBrowser bool,
	snapshotInterval, fastSyncHeight int64,
	fastSyncHash, stateSyncRPCServers string,
) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	fs.StringVar(&network, "network", "https://node.chakram.one", "fallback bootstrap URL")
	fs.StringVar(&seeds, "seeds", defaultSeedsDNS, "comma-separated DNS seed names")
	fs.StringVar(&dataDir, "data-dir", defaultDataDir(), "data directory")
	fs.StringVar(&reward, "reward", "", "EVM address for block rewards (auto-generated if empty)")
	fs.BoolVar(&noBrowser, "no-browser", false, "don't open browser on startup")
	fs.Int64Var(&snapshotInterval, "snapshot-interval", 0, "blocks between snapshots (0 = disabled)")
	fs.Int64Var(&fastSyncHeight, "trusted-height", 0, "snapshot height to fast-sync from (requires --trusted-hash and pre-imported geth data)")
	fs.StringVar(&fastSyncHash, "trusted-hash", "", "EVM block hash at --trusted-height (0x-prefixed)")
	fs.StringVar(&stateSyncRPCServers, "rpc-servers", "", "comma-separated CometBFT RPC URLs for state-sync hash verification")
	_ = fs.Parse(args)
	return
}
