//go:build darwin

package main

import (
	"syscall"
	"unsafe"
)

func sysTotalMemory() uint64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(s) < 8 {
		return 0
	}
	// sysctl returns a binary value; reinterpret as uint64.
	b := []byte(s)
	if len(b) < 8 {
		return 0
	}
	return *(*uint64)(unsafe.Pointer(&b[0]))
}
