//go:build windows

package process

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
	}
}

func sendTermSignal(proc *os.Process) {
	// Windows has no SIGTERM for external processes; use Kill directly.
	proc.Kill() //nolint:errcheck
}

func signalProcess(proc *os.Process) bool {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(proc.Pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}

// exePathOfPID returns the executable path for a Windows PID via wmic.
func exePathOfPID(pid int) (string, error) {
	out, err := exec.Command("wmic", "process", "where",
		fmt.Sprintf("ProcessId=%d", pid),
		"get", "ExecutablePath", "/format:value").Output()
	if err != nil {
		return "", fmt.Errorf("exePathOfPID(%d): %w", pid, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecutablePath=") {
			path := strings.TrimPrefix(line, "ExecutablePath=")
			path = strings.TrimSpace(path)
			if path != "" {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("exePathOfPID(%d): not found", pid)
}

// killOnPort kills a process on port only if it is verifiably ours.
// Returns a user-friendly error for foreign processes.
func killOnPort(port int, knownPIDs map[int]string, ownedBins []string) error {
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return nil
	}
	myPID := os.Getpid()
	target := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, target) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 || pid == myPID {
			continue
		}

		exePath, exeErr := exePathOfPID(pid)

		if exeErr == nil {
			if expectedBin, ok := knownPIDs[pid]; ok && strings.EqualFold(expectedBin, exePath) {
				log.Printf("[manager] killOnPort: killing own orphan pid=%d (pidfile match) on port %d", pid, port)
				exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run() //nolint:errcheck
				continue
			}
		}

		if exeErr == nil {
			for _, bin := range ownedBins {
				if strings.EqualFold(bin, exePath) {
					log.Printf("[manager] killOnPort: killing own orphan pid=%d (bin match) on port %d", pid, port)
					exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run() //nolint:errcheck
					goto next
				}
			}
		}

		return fmt.Errorf("port %d is already in use by another program (PID %d). "+
			"Please stop it before starting the miner.", port, pid)

	next:
	}
	return nil
}
