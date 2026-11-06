//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func notifySignals(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
}

func stopProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

func isProcessRunning(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
