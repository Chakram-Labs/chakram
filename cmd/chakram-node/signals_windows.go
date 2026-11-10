//go:build windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func notifySignals(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGINT)
}

func stopProcess(proc *os.Process) error {
	return proc.Kill()
}

func isProcessRunning(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	// On Windows, sending signal 0 isn't a standard way to check process liveness.
	// Instead, try to open the process — if it fails, it's gone.
	handle, err := os.FindProcess(proc.Pid)
	if err != nil {
		return false
	}
	// A zero-value kill attempt to check if process is alive.
	err = handle.Signal(syscall.Signal(0))
	return err == nil
}
