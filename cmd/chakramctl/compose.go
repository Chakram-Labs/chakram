package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// composeRun runs `docker compose <args...>` in projectDir.
// stdout/stderr are forwarded to the terminal.
func composeRun(projectDir string, args ...string) error {
	composePath := findCompose(projectDir)
	if composePath == "" {
		return fmt.Errorf("docker-compose.yml not found in %s", projectDir)
	}

	cmdArgs := append([]string{"compose", "-f", composePath}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// composeOutput runs `docker compose <args...>` and returns stdout as a string.
func composeOutput(projectDir string, args ...string) (string, error) {
	composePath := findCompose(projectDir)
	if composePath == "" {
		return "", fmt.Errorf("docker-compose.yml not found in %s", projectDir)
	}

	cmdArgs := append([]string{"compose", "-f", composePath}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// findCompose looks for docker-compose.yml or docker-compose.yaml in dir.
func findCompose(dir string) string {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "docker/docker-compose.yml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ensureDockerRunning checks that the docker daemon is reachable.
func ensureDockerRunning() error {
	cmd := exec.Command("docker", "info", "--format", "{{.ServerVersion}}")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker daemon not reachable — is Docker running? (%w)", err)
	}
	return nil
}
