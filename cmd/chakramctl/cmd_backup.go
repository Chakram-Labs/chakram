package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func cmdBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	_ = projectDirFlag(fs)
	outFile := fs.String("out", "", "output archive path (default: ./backup-<timestamp>.tar.gz)")
	mode := fs.String("mode", "dev", "node mode: dev, localnet")
	_ = fs.Parse(args)

	if err := ensureDockerRunning(); err != nil {
		fatalf("%v", err)
	}

	dest := *outFile
	if dest == "" {
		dest = fmt.Sprintf("backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	fmt.Printf("Creating backup: %s\n", dest)

	volumes := modeVolumes(*mode)
	dockerArgs := []string{"run", "--rm"}
	for _, v := range volumes {
		mountName := strings.TrimPrefix(v, "chakram_")
		dockerArgs = append(dockerArgs, "-v", v+":/backup/"+mountName+":ro")
	}
	dockerArgs = append(dockerArgs, "alpine", "tar", "-czf", "-", "-C", "/backup", ".")

	cmd := exec.Command("docker", dockerArgs...)
	outCmd := exec.Command("sh", "-c", "cat > "+dest)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		fatalf("pipe: %v", err)
	}
	outCmd.Stdin = pipe

	if err := cmd.Start(); err != nil {
		fatalf("docker run: %v", err)
	}
	if err := outCmd.Run(); err != nil {
		fatalf("write archive: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		fatalf("tar: %v", err)
	}

	fmt.Printf("Backup complete: %s\n", dest)
}

// modeVolumes returns the named volumes for a given node mode.
func modeVolumes(mode string) []string {
	switch mode {
	case "localnet":
		var vols []string
		for i := 0; i < 4; i++ {
			vols = append(vols, fmt.Sprintf("chakram_geth-node%d-data", i))
			vols = append(vols, fmt.Sprintf("chakram_chakram-node%d-data", i))
		}
		return vols
	case "fullnode":
		return []string{"chakram_geth-fullnode-data", "chakram_chakram-fullnode-data"}
	default: // dev
		return []string{"chakram_geth-dev-data", "chakram_chakram-dev-data"}
	}
}
