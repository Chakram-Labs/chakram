//go:build !windows

package process

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func sendTermSignal(proc *os.Process) {
	proc.Signal(syscall.SIGTERM) //nolint:errcheck
}

func signalProcess(proc *os.Process) bool {
	return proc.Signal(syscall.Signal(0)) == nil
}

// exePathOfPID returns the absolute path of the executable for the given PID.
// Tries /proc/PID/exe first (Linux), then falls back to lsof (macOS).
func exePathOfPID(pid int) (string, error) {
	// Linux: /proc/<pid>/exe is a symlink to the binary.
	if path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return path, nil
	}
	// macOS: query lsof for the process's text segment path.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-p", strconv.Itoa(pid), "-a", "-d", "txt", "-Fn").Output()
	if err != nil {
		return "", fmt.Errorf("exePathOfPID(%d): %w", pid, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "n/") {
			return line[1:], nil
		}
	}
	return "", fmt.Errorf("exePathOfPID(%d): not found in lsof output", pid)
}

// killOnPort kills a process listening on port only if we can verify it belongs
// to us (PID in knownPIDs with a matching exe path, or exe path in ownedBins).
// Returns a user-friendly error if the port is held by a foreign process.
func killOnPort(port int, knownPIDs map[int]string, ownedBins []string) error {
	log.Printf("[manager] killOnPort: checking port %d", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-ti", fmt.Sprintf("tcp:%d", port)).Output()
	if err != nil {
		return nil // nothing on this port
	}
	myPID := os.Getpid()
	for _, line := range strings.Fields(strings.TrimSpace(string(out))) {
		pid, err := strconv.Atoi(line)
		if err != nil || pid == myPID {
			continue
		}

		exePath, exeErr := exePathOfPID(pid)

		// Ownership check 1: PID recorded in our pidfile with a matching exe path.
		if exeErr == nil {
			if expectedBin, ok := knownPIDs[pid]; ok && expectedBin == exePath {
				log.Printf("[manager] killOnPort: killing own orphan pid=%d (pidfile match) on port %d", pid, port)
				if p, err := os.FindProcess(pid); err == nil {
					p.Signal(syscall.SIGKILL) //nolint:errcheck
				}
				continue
			}
		}

		// Ownership check 2: exe path matches one of our bundled binaries (crash
		// without a pidfile, or pidfile was cleared before the process exited).
		if exeErr == nil {
			for _, bin := range ownedBins {
				if bin == exePath {
					log.Printf("[manager] killOnPort: killing own orphan pid=%d (bin match) on port %d", pid, port)
					if p, err := os.FindProcess(pid); err == nil {
						p.Signal(syscall.SIGKILL) //nolint:errcheck
					}
					goto next
				}
			}
		}

		// Unknown process — do not kill; surface a clear error instead.
		return fmt.Errorf("port %d is already in use by another program (PID %d). "+
			"Please stop it before starting the miner.", port, pid)

	next:
	}
	log.Printf("[manager] killOnPort: done port %d", port)
	return nil
}
