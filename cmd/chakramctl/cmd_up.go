package main

import (
	"flag"
	"fmt"
)

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	mode := fs.String("mode", "dev", "node mode: dev, localnet, fullnode")
	monitoring := fs.Bool("monitoring", false, "also start Prometheus + Grafana")
	detach := fs.Bool("d", true, "run in background (detached)")
	_ = fs.Parse(args)

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	dir := *projectDir
	profile := modeToProfile(*mode)

	composeArgs := []string{"--profile", profile}
	if *monitoring {
		composeArgs = append(composeArgs, "--profile", "monitoring")
	}
	composeArgs = append(composeArgs, "up")
	if *detach {
		composeArgs = append(composeArgs, "-d")
	}
	composeArgs = append(composeArgs, "--remove-orphans")

	fmt.Printf("Starting chakram node (mode=%s)...\n", *mode)
	if err := composeRun(dir, composeArgs...); err != nil {
		fatalf("docker compose up: %v", err)
	}

	fmt.Printf("\nNode is running. Use:\n")
	fmt.Printf("  chakramctl status    — check node status\n")
	fmt.Printf("  chakramctl logs      — follow logs\n")
	fmt.Printf("  chakramctl down      — stop the node\n")
}
