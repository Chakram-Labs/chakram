package main

import (
	"flag"
	"fmt"
)

func cmdDown(args []string) {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	removeVolumes := fs.Bool("v", false, "also remove named volumes (destroys chain data)")
	_ = fs.Parse(args)

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	dir := *projectDir
	downArgs := []string{"down"}
	if *removeVolumes {
		downArgs = append(downArgs, "-v")
		fmt.Println("WARNING: --v removes all chain data volumes. This cannot be undone.")
	}

	if err := composeRun(dir, downArgs...); err != nil {
		fatalf("docker compose down: %v", err)
	}
}
