package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// mockPeersServer returns an httptest.Server whose GET /peers handler replies with addrs.
func mockPeersServer(t *testing.T, addrs []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PeersResponse{Peers: addrs}) //nolint:errcheck
	}))
}

// routableURL builds a unique public-looking URL in 1.0.0.0/8.
func routableURL(i int) string {
	return fmt.Sprintf("http://%d.%d.%d.%d:9101", 1+i/16777216, (i/65536)%256, (i/256)%256, i%256)
}

// ── isRoutable ────────────────────────────────────────────────────────────────

func TestIsRoutable(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://34.14.218.57:9101", true},
		{"http://seed.chakram.one:9101", true},
		{"https://seed.chakram.one:9101", true},
		{"http://127.0.0.1:9101", false},
		{"http://10.0.0.1:9101", false},
		{"http://172.16.5.10:9101", false},
		{"http://192.168.1.100:9101", false},
		{"http://169.254.1.1:9101", false},
		{"ftp://34.14.218.57:9101", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		if got := isRoutable(tt.url); got != tt.want {
			t.Errorf("isRoutable(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// ── Seed: trusted source, no routability filter ───────────────────────────────

func TestSeedAcceptsAll(t *testing.T) {
	pm := NewPeerManager("", 0, "")
	// Private IPs from Seed() are accepted — used for devnet/local testing.
	pm.Seed([]string{"http://127.0.0.1:9101", "http://34.14.218.57:9101"})
	if pm.Count() != 2 {
		t.Errorf("Seed: got %d peers, want 2", pm.Count())
	}
}

// ── Private/loopback addresses from peer exchange are rejected ────────────────

func TestExchangeFiltersPrivateAddresses(t *testing.T) {
	private := []string{
		"http://192.168.1.1:9101",
		"http://10.0.0.1:9101",
		"http://127.0.0.1:9101",
		"http://172.16.5.10:9101",
		"http://169.254.1.1:9101",
	}
	srv := mockPeersServer(t, private)
	defer srv.Close()

	pm := NewPeerManager("", 0, "")
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	added := pm.ExchangeFrom(ctx, NewClient(srv.URL))

	if added != 0 {
		t.Errorf("ExchangeFrom accepted %d private addresses, want 0", added)
	}
}

// ── Malicious peer advertising 10,000 addresses is capped ────────────────────

func TestFloodingPeerCapped(t *testing.T) {
	flood := make([]string, 10_000)
	for i := range flood {
		flood[i] = routableURL(i + 1)
	}
	srv := mockPeersServer(t, flood)
	defer srv.Close()

	pm := NewPeerManager("", 0, "")
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	added := pm.ExchangeFrom(ctx, NewClient(srv.URL))

	if added > maxFromExchange {
		t.Errorf("accepted %d addresses from flood peer, want <= %d", added, maxFromExchange)
	}
	if pm.Count() > maxFromExchange+1 { // +1 for the seed server itself
		t.Errorf("stored %d peers total, want <= %d", pm.Count(), maxFromExchange+1)
	}
}

// ── Total capacity cap with eviction ─────────────────────────────────────────

func TestCapacityCapWithEviction(t *testing.T) {
	pm := NewPeerManager("", 0, "")
	urls := make([]string, maxStoredPeers+200)
	for i := range urls {
		urls[i] = routableURL(i + 1)
	}
	pm.Seed(urls)
	if pm.Count() > maxStoredPeers {
		t.Errorf("stored %d peers, want <= %d", pm.Count(), maxStoredPeers)
	}
}

// ── Subnet /16 cap limits peers from the same range ──────────────────────────

func TestSubnet16CapEnforced(t *testing.T) {
	// Build more than maxPerSubnet16 peers in the same /16 (34.14.x.x).
	same16 := make([]string, maxPerSubnet16+5)
	for i := range same16 {
		same16[i] = fmt.Sprintf("http://34.14.%d.%d:9101", i, i)
	}
	srv := mockPeersServer(t, same16)
	defer srv.Close()

	pm := NewPeerManager("", 0, "")
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pm.ExchangeFrom(ctx, NewClient(srv.URL))

	pm.mu.RLock()
	count := pm.subnet16s["34.14"]
	pm.mu.RUnlock()

	if count > maxPerSubnet16 {
		t.Errorf("subnet 34.14 has %d peers, want <= %d", count, maxPerSubnet16)
	}
}

// ── GET /peers returns only verified peers ────────────────────────────────────

func TestURLsOnlyVerified(t *testing.T) {
	pm := NewPeerManager("", 0, "")
	srv := mockPeersServer(t, []string{routableURL(1), routableURL(2)})
	defer srv.Close()
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pm.ExchangeFrom(ctx, NewClient(srv.URL))

	// No verification done yet — URLs() must be empty.
	if urls := pm.URLs(); len(urls) != 0 {
		t.Errorf("URLs() returned %d entries before verification, want 0", len(urls))
	}
}

// ── Handshake: wrong chainID removes the peer ─────────────────────────────────

func TestHandshakeWrongChainIDRemovesPeer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(StatusResponse{Height: 1, ChainID: 9999}) //nolint:errcheck
	}))
	defer srv.Close()

	pm := NewPeerManager("", 11640, "")
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pm.verifyNew(ctx)

	if pm.Count() != 0 {
		t.Errorf("peer with wrong chainID should be removed, got %d peer(s)", pm.Count())
	}
}

// ── Handshake: correct chainID + genesis promotes to verified ─────────────────

func TestHandshakeCorrectChainVerifies(t *testing.T) {
	const genesis = "0xdeadbeef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(StatusResponse{Height: 5, ChainID: 11640, GenesisHash: genesis}) //nolint:errcheck
	}))
	defer srv.Close()

	pm := NewPeerManager("", 11640, genesis)
	pm.Seed([]string{srv.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pm.verifyNew(ctx)

	if pm.Count() != 1 {
		t.Fatalf("expected 1 peer after verification, got %d", pm.Count())
	}
	if urls := pm.URLs(); len(urls) != 1 {
		t.Errorf("URLs() should return 1 verified peer, got %d", len(urls))
	}
}

// ── GET /peers response is capped at maxPeersPerResponse ─────────────────────

func TestURLsResponseCap(t *testing.T) {
	pm := NewPeerManager("", 0, "")
	pm.mu.Lock()
	for i := 0; i < maxPeersPerResponse+50; i++ {
		u := routableURL(i + 1)
		s := subnet16(u)
		pm.peers[u] = &peerEntry{url: u, subnet16: s, state: peerVerified, addedAt: time.Now()}
		pm.subnet16s[s]++
	}
	pm.mu.Unlock()

	if urls := pm.URLs(); len(urls) > maxPeersPerResponse {
		t.Errorf("URLs() returned %d entries, want <= %d", len(urls), maxPeersPerResponse)
	}
}

// ── SaveToDisk only persists verified peers ───────────────────────────────────

func TestSaveToDiskOnlyVerified(t *testing.T) {
	tmp := t.TempDir()
	store := tmp + "/peers.json"
	pm := NewPeerManager(store, 0, "")

	pm.mu.Lock()
	pm.peers["http://1.2.3.4:9101"] = &peerEntry{url: "http://1.2.3.4:9101", subnet16: "1.2", state: peerVerified, addedAt: time.Now()}
	pm.subnet16s["1.2"]++
	pm.peers["http://5.6.7.8:9101"] = &peerEntry{url: "http://5.6.7.8:9101", subnet16: "5.6", state: peerUnverified, addedAt: time.Now()}
	pm.subnet16s["5.6"]++
	pm.mu.Unlock()

	pm.SaveToDisk()

	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var saved []string
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(saved) != 1 || saved[0] != "http://1.2.3.4:9101" {
		t.Errorf("SaveToDisk saved %v, want only [http://1.2.3.4:9101]", saved)
	}
}
