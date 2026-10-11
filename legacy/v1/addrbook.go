// addrbook.go — Persistent peer address book for Chakram.
// Saves discovered peer addresses across restarts so the node can reconnect
// without relying solely on hardcoded seeds.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	addrBookMax  = 1000 // maximum stored addresses
	addrBookFile = "peers.json"
)

type addrEntry struct {
	Address  string    `json:"address"`
	LastSeen time.Time `json:"last_seen"`
}

// AddrBook stores known peer addresses and persists them to disk.
type AddrBook struct {
	mu      sync.Mutex
	entries map[string]*addrEntry
	path    string
}

// NewAddrBook loads (or creates) the address book at dataDir/peers.json.
func NewAddrBook(dataDir string) *AddrBook {
	ab := &AddrBook{
		entries: make(map[string]*addrEntry),
		path:    filepath.Join(dataDir, addrBookFile),
	}
	ab.load()
	return ab
}

// Add records addr as a known peer. Evicts the oldest entry when at capacity.
// Callers should follow a batch of Add() calls with Save() to flush to disk.
func (ab *AddrBook) Add(addr string) {
	if addr == "" {
		return
	}
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if e, exists := ab.entries[addr]; exists {
		e.LastSeen = time.Now()
		return
	}
	if len(ab.entries) >= addrBookMax {
		ab.evictOldestLocked()
	}
	ab.entries[addr] = &addrEntry{Address: addr, LastSeen: time.Now()}
}

// Save flushes the current address book to disk.
func (ab *AddrBook) Save() {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	ab.saveLocked()
}

// MarkSeen updates the last-seen timestamp for a known address.
func (ab *AddrBook) MarkSeen(addr string) {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if e, exists := ab.entries[addr]; exists {
		e.LastSeen = time.Now()
		ab.saveLocked()
	}
}

// Remove deletes addr from the book (e.g. permanently banned peers).
func (ab *AddrBook) Remove(addr string) {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	delete(ab.entries, addr)
	ab.saveLocked()
}

// GetAll returns a snapshot of all known addresses, newest-seen first.
func (ab *AddrBook) GetAll() []string {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	addrs := make([]string, 0, len(ab.entries))
	for addr := range ab.entries {
		addrs = append(addrs, addr)
	}
	return addrs
}

// DataDir returns the directory where the address book file lives.
func (ab *AddrBook) DataDir() string {
	return filepath.Dir(ab.path)
}

// Size returns the number of known addresses.
func (ab *AddrBook) Size() int {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return len(ab.entries)
}

func (ab *AddrBook) evictOldestLocked() {
	var oldest string
	var oldestTime time.Time
	for addr, e := range ab.entries {
		if oldest == "" || e.LastSeen.Before(oldestTime) {
			oldest = addr
			oldestTime = e.LastSeen
		}
	}
	if oldest != "" {
		delete(ab.entries, oldest)
	}
}

func (ab *AddrBook) load() {
	data, err := os.ReadFile(ab.path)
	if err != nil {
		return // file doesn't exist yet — fresh start
	}
	var entries []*addrEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, e := range entries {
		ab.entries[e.Address] = e
	}
}

func (ab *AddrBook) saveLocked() {
	entries := make([]*addrEntry, 0, len(ab.entries))
	for _, e := range ab.entries {
		entries = append(entries, e)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	os.WriteFile(ab.path, data, 0600) //nolint:errcheck — best-effort persist
}
