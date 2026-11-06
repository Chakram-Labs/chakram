package main

import (
	"flag"
	"fmt"
)

func cmdReset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	_ = fs.Parse(args)

	if !*yes {
		fmt.Println("WARNING: This will wipe all chain data (CometBFT blockstore + geth state).")
		fmt.Println("Keys and configuration are preserved.")
		fmt.Println("Type 'yes' to confirm:")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	// Stop running containers first.
	_ = composeRun(*projectDir, "down")

	// Remove data volumes but keep keys volume.
	if err := composeRun(*projectDir, "down", "--volumes"); err != nil {
		fmt.Println("Note: some volumes could not be removed (may not exist yet).")
	}

	fmt.Println("Chain data wiped. Keys and config preserved.")
	fmt.Println("Run 'chakramctl up' to start fresh.")
}
