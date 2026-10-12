package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Addrman-style caps — keep these conservative.
const (
	maxStoredPeers      = 1000 // total peer set (verified + unverified); evict when full
	maxPeersPerResponse = 100  // cap for GET /peers so we can't be used for amplification
	maxFromExchange     = 50   // max new addresses accepted per exchange call per peer
	maxPerSubnet16      = 3    // max stored peers sharing a /16 subnet (flood protection)
)

// peerState tracks whether a peer has passed a full handshake.
type peerState uint8

const (
	peerUnverified peerState = iota
	peerVerified
)

type peerEntry struct {
	url      string
	subnet16 string    // first two octets of the IP, e.g. "34.14"
	state    peerState
	addedAt  time.Time
}

// privateBlocks lists IP ranges that are never routable on the public internet.
var privateBlocks = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",   // loopback
		"10.0.0.0/8",    // RFC1918
		"172.16.0.0/12", // RFC1918
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // link-local
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 ULA
		"fe80::/10",      // IPv6 link-local
	}
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, _ := net.ParseCIDR(cidr)
		blocks = append(blocks, block)
	}
	return blocks
}()

// isRoutable returns true when rawURL carries a public, internet-reachable address.
// Hostnames (not raw IPs) are assumed routable. Private/loopback IPs are rejected.
func isRoutable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // hostname — assume routable
	}
	for _, block := range privateBlocks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// subnet16 returns the /16 subnet key for an IP-addressed URL ("A.B").
// For hostname URLs, returns the hostname itself.
func subnet16(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return host
	}
	return fmt.Sprintf("%d.%d", ip[0], ip[1])
}

// PeerManager maintains a dynamic set of known consensus-layer peer URLs.
// Bootstrap order: explicit Seed() calls → DNS TXT → disk cache.
// Newly discovered peers are held as unverified until a successful
// GET /status handshake confirms they speak the same chain.
type PeerManager struct {
	mu          sync.RWMutex
	peers       map[string]*peerEntry
	subnet16s   map[string]int // count of all stored peers per /16 (not just verified)
	storeFile   string
	chainID     uint64
	genesisHash string
}

// NewPeerManager creates a PeerManager. storeFile may be empty to disable disk persistence.
// chainID and genesisHash are used to validate peers during handshake verification.
func NewPeerManager(storeFile string, chainID uint64, genesisHash string) *PeerManager {
	return &PeerManager{
		peers:       make(map[string]*peerEntry),
		subnet16s:   make(map[string]int),
		storeFile:   storeFile,
		chainID:     chainID,
		genesisHash: genesisHash,
	}
}

// Seed adds URLs from trusted sources (DNS resolution, --peers flag, disk store).
// No routability filter — callers are responsible for providing sane URLs.
func (pm *PeerManager) Seed(urls []string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, exists := pm.peers[u]; exists {
			continue
		}
		if len(pm.peers) >= maxStoredPeers {
			pm.evictLocked()
		}
		s := subnet16(u)
		pm.peers[u] = &peerEntry{url: u, subnet16: s, state: peerUnverified, addedAt: time.Now()}
		pm.subnet16s[s]++
	}
}

// LoadDNS queries DNS TXT records on domain for peer URLs and Seed()s them.
// Returns number of new peers added (0 on lookup failure — not fatal).
func (pm *PeerManager) LoadDNS(domain string) int {
	if domain == "" {
		return 0
	}
	txts, err := net.LookupTXT(domain)
	if err != nil {
		log.Printf("[p2p] DNS seed %s: %v (using fallback peers)", domain, err)
		return 0
	}
	var urls []string
	for _, txt := range txts {
		for _, u := range strings.Fields(txt) {
			u = strings.TrimSpace(u)
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				urls = append(urls, u)
			}
		}
	}
	before := pm.Count()
	pm.Seed(urls)
	added := pm.Count() - before
	log.Printf("[p2p] DNS seed %s: +%d peer(s) (total: %d)", domain, added, pm.Count())
	return added
}

// LoadDNSA looks up A records for domain, builds http://ip:port URLs, and Seeds them.
// Used as a fallback when no TXT records exist.
func (pm *PeerManager) LoadDNSA(domain string, port int) int {
	if domain == "" {
		return 0
	}
	addrs, err := net.LookupHost(domain)
	if err != nil {
		log.Printf("[p2p] DNS A %s: %v", domain, err)
		return 0
	}
	var urls []string
	for _, addr := range addrs {
		urls = append(urls, fmt.Sprintf("http://%s:%d", addr, port))
	}
	before := pm.Count()
	pm.Seed(urls)
	added := pm.Count() - before
	if added > 0 {
		log.Printf("[p2p] DNS A %s: +%d peer(s)", domain, added)
	}
	return added
}

// LoadFromDisk adds previously saved verified peers. Silent no-op on first run.
func (pm *PeerManager) LoadFromDisk() {
	if pm.storeFile == "" {
		return
	}
	data, err := os.ReadFile(pm.storeFile)
	if err != nil {
		return
	}
	var peers []string
	if err := json.Unmarshal(data, &peers); err != nil {
		return
	}
	before := pm.Count()
	pm.Seed(peers)
	if added := pm.Count() - before; added > 0 {
		log.Printf("[p2p] peer store: +%d cached peer(s)", added)
	}
}

// SaveToDisk persists only verified peers to disk.
func (pm *PeerManager) SaveToDisk() {
	if pm.storeFile == "" {
		return
	}
	pm.mu.RLock()
	peers := make([]string, 0, len(pm.peers))
	for _, e := range pm.peers {
		if e.state == peerVerified {
			peers = append(peers, e.url)
		}
	}
	pm.mu.RUnlock()

	data, _ := json.MarshalIndent(peers, "", "  ")
	_ = os.MkdirAll(filepath.Dir(pm.storeFile), 0755)
	_ = os.WriteFile(pm.storeFile, data, 0644)
}

// URLs returns up to maxPeersPerResponse verified peer URLs in random order.
// Only verified peers are shared — we never advertise addresses we haven't handshaked.
func (pm *PeerManager) URLs() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	urls := make([]string, 0, len(pm.peers))
	for _, e := range pm.peers {
		if e.state == peerVerified {
			urls = append(urls, e.url)
		}
	}
	if len(urls) > maxPeersPerResponse {
		rand.Shuffle(len(urls), func(i, j int) { urls[i], urls[j] = urls[j], urls[i] })
		urls = urls[:maxPeersPerResponse]
	}
	return urls
}

// ActiveClients returns p2p.Client instances for ALL known peers (verified and unverified).
// Used for relay and sync — unverified peers will simply fail their requests gracefully.
func (pm *PeerManager) ActiveClients() []*Client {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	clients := make([]*Client, 0, len(pm.peers))
	for _, e := range pm.peers {
		clients = append(clients, NewClient(e.url))
	}
	return clients
}

// Count returns the total number of known peers (verified + unverified).
func (pm *PeerManager) Count() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.peers)
}

// exchangeFrom fetches GET /peers from c, applies addrman caps, and queues new URLs
// as unverified. Returns the number of new addresses added.
// Exported so tests can call it directly without running the full loop.
func (pm *PeerManager) ExchangeFrom(ctx context.Context, c *Client) int {
	peers, err := c.FetchPeers(ctx)
	if err != nil {
		return 0
	}
	// Hard cap per exchange call — malicious peer cannot flood our set.
	if len(peers) > maxFromExchange {
		peers = peers[:maxFromExchange]
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	added := 0
	for _, u := range peers {
		if pm.tryAddLocked(u) {
			added++
		}
	}
	return added
}

// tryAddLocked adds u as unverified, applying routability, subnet-cap, and total-cap checks.
// Must be called with pm.mu held for writing.
func (pm *PeerManager) tryAddLocked(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if !isRoutable(u) {
		return false
	}
	if _, exists := pm.peers[u]; exists {
		return false
	}
	s := subnet16(u)
	if pm.subnet16s[s] >= maxPerSubnet16 {
		return false
	}
	if len(pm.peers) >= maxStoredPeers {
		pm.evictLocked()
	}
	pm.peers[u] = &peerEntry{url: u, subnet16: s, state: peerUnverified, addedAt: time.Now()}
	pm.subnet16s[s]++
	return true
}

// evictLocked removes one peer — preferring unverified, then oldest verified.
// Must be called with pm.mu held for writing.
func (pm *PeerManager) evictLocked() {
	for u, e := range pm.peers {
		if e.state == peerUnverified {
			pm.removeLocked(u)
			return
		}
	}
	var oldest string
	var oldestTime time.Time
	for u, e := range pm.peers {
		if oldest == "" || e.addedAt.Before(oldestTime) {
			oldest = u
			oldestTime = e.addedAt
		}
	}
	if oldest != "" {
		pm.removeLocked(oldest)
	}
}

// removeLocked deletes a peer and updates the subnet count.
// Must be called with pm.mu held for writing.
func (pm *PeerManager) removeLocked(u string) {
	if e, ok := pm.peers[u]; ok {
		pm.subnet16s[e.subnet16]--
		if pm.subnet16s[e.subnet16] <= 0 {
			delete(pm.subnet16s, e.subnet16)
		}
		delete(pm.peers, u)
	}
}

// verifyNew attempts a GET /status handshake with every unverified peer.
// Peers that match chainID + genesisHash are promoted to verified.
// Peers that respond with a wrong chain are removed.
// Peers that don't respond are left as unverified for the next round.
func (pm *PeerManager) verifyNew(ctx context.Context) {
	pm.mu.RLock()
	unverified := make([]string, 0)
	for u, e := range pm.peers {
		if e.state == peerUnverified {
			unverified = append(unverified, u)
		}
	}
	pm.mu.RUnlock()

	for _, u := range unverified {
		vCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		status, err := NewClient(u).FetchStatus(vCtx)
		cancel()
		if err != nil {
			continue // unreachable — leave unverified, retry later
		}
		pm.mu.Lock()
		if pm.chainID != 0 && status.ChainID != 0 && status.ChainID != pm.chainID {
			log.Printf("[p2p] peer %s removed: wrong chainID %d (want %d)", u, status.ChainID, pm.chainID)
			pm.removeLocked(u)
			pm.mu.Unlock()
			continue
		}
		if pm.genesisHash != "" && status.GenesisHash != "" &&
			!strings.EqualFold(status.GenesisHash, pm.genesisHash) {
			log.Printf("[p2p] peer %s removed: wrong genesis %s", u, status.GenesisHash)
			pm.removeLocked(u)
			pm.mu.Unlock()
			continue
		}
		if e, ok := pm.peers[u]; ok {
			e.state = peerVerified
		}
		pm.mu.Unlock()
	}
}

// ExchangeLoop runs forever, periodically exchanging peer lists and verifying
// newly discovered peers. Returns when ctx is cancelled.
func (pm *PeerManager) ExchangeLoop(ctx context.Context) {
	do := func() {
		// 1. Exchange with all currently known peers (verified and unverified).
		clients := pm.ActiveClients()
		newPeers := 0
		for _, c := range clients {
			eCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			newPeers += pm.ExchangeFrom(eCtx, c)
			cancel()
		}
		// 2. Verify newly added (and any still-unverified) peers.
		pm.verifyNew(ctx)
		// 3. Persist verified peers to disk.
		if newPeers > 0 {
			log.Printf("[p2p] peer exchange: +%d candidate(s) (total: %d)", newPeers, pm.Count())
			pm.SaveToDisk()
		}
	}

	do() // run immediately at startup

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			do()
		}
	}
}
