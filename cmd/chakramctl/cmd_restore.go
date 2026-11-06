package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"
)

func cmdRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	archive := fs.String("from", "", "backup archive path (required)")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	mode := fs.String("mode", "dev", "node mode: dev, localnet, fullnode")
	_ = fs.Parse(args)

	if *archive == "" {
		fatalf("--from <archive> is required")
	}

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	if !*yes {
		fmt.Println("WARNING: This will overwrite current chain data with the backup.")
		fmt.Println("Type 'yes' to confirm:")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	// Stop the node first.
	_ = composeRun(*projectDir, "down")

	fmt.Printf("Restoring from %s...\n", *archive)

	volumes := modeVolumes(*mode)
	dockerArgs := []string{"run", "--rm", "-i"}
	for _, v := range volumes {
		mountName := strings.TrimPrefix(v, "chakram_")
		// Create the volume if it doesn't exist yet.
		_ = exec.Command("docker", "volume", "create", v).Run()
		dockerArgs = append(dockerArgs, "-v", v+":/restore/"+mountName)
	}
	dockerArgs = append(dockerArgs, "alpine", "sh", "-c", "cd /restore && tar -xzf -")

	cmd := exec.Command("docker", dockerArgs...)
	// Feed the archive to stdin.
	catCmd := exec.Command("cat", *archive)
	pipe, err := catCmd.StdoutPipe()
	if err != nil {
		fatalf("pipe: %v", err)
	}
	cmd.Stdin = pipe

	if err := catCmd.Start(); err != nil {
		fatalf("cat: %v", err)
	}
	if err := cmd.Run(); err != nil {
		fatalf("restore: %v", err)
	}
	if err := catCmd.Wait(); err != nil {
		fatalf("read archive: %v", err)
	}

	fmt.Println("Restore complete. Run 'chakramctl up' to start the node.")
}
