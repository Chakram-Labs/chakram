//go:build !windows

package process

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// processNameOfPID returns the bare executable name (e.g. "geth") for the given
// PID using ps. Used as a fallback when exePathOfPID fails.
func processNameOfPID(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	// ps -o comm= returns the last path component only.
	name := strings.TrimSpace(string(out))
	return filepath.Base(name)
}

// sameFile returns true if paths a and b refer to the same file after resolving
// symlinks. Falls back to string equality if EvalSymlinks fails.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return false
}

// killOnPort kills a process listening on port only if we can verify it belongs
// to us. Ownership is checked in order:
//  1. PID in knownPIDs with a matching exe path (pidfile match).
//  2. Exe path matches one of our bundled binaries (orphan after crash).
//  3. Exe path unavailable (lsof/SIP failure) but process name matches a bundled
//     binary name — safe to kill since ports 8545/8551/9101 are exclusively ours.
//
// Returns a user-friendly error if the port is held by a clearly foreign process.
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

		// Check 1: PID recorded in our pidfile with a matching exe path.
		if exeErr == nil {
			if expectedBin, ok := knownPIDs[pid]; ok && sameFile(expectedBin, exePath) {
				log.Printf("[manager] killOnPort: killing own orphan pid=%d (pidfile match) on port %d", pid, port)
				if p, err := os.FindProcess(pid); err == nil {
					p.Signal(syscall.SIGKILL) //nolint:errcheck
				}
				continue
			}
		}

		// Check 2: exe path matches one of our bundled binaries.
		if exeErr == nil {
			for _, bin := range ownedBins {
				if sameFile(bin, exePath) {
					log.Printf("[manager] killOnPort: killing own orphan pid=%d (bin match) on port %d", pid, port)
					if p, err := os.FindProcess(pid); err == nil {
						p.Signal(syscall.SIGKILL) //nolint:errcheck
					}
					goto next
				}
			}
		}

		// Check 3: exe path unavailable (lsof permission denied / SIP / macOS sandbox).
		// Fall back to process name. Ports 8545/8551/9101 are exclusively ours, so a
		// process named "geth" or "chakram" here is virtually always our own orphan.
		if exeErr != nil {
			procName := processNameOfPID(pid)
			for _, bin := range ownedBins {
				if filepath.Base(bin) == procName {
					log.Printf("[manager] killOnPort: killing orphan pid=%d name=%q (name match, lsof unavailable) on port %d",
						pid, procName, port)
					if p, err := os.FindProcess(pid); err == nil {
						p.Signal(syscall.SIGKILL) //nolint:errcheck
					}
					goto next
				}
			}
		}

		// Foreign process — do not kill; surface a clear error.
		return fmt.Errorf("port %d is in use by another program (PID %d). "+
			"Stop it first: kill %d", port, pid, pid)

	next:
	}
	log.Printf("[manager] killOnPort: done port %d", port)
	return nil
}
