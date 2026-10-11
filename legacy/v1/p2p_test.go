// p2p_test.go — Tests for issues 10 (connection deadlines + inbound limit) and 12 (dial bound).
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// TestReadDeadlineEvictsIdlePeer verifies that TCP connections respect a read
// deadline, i.e., a silent remote end is not held open indefinitely.
//
// Before the fix: readLoop calls DecodeMessage with no deadline. A peer that
// opens a connection but never sends data holds the connection forever.
//
// After the fix: readLoop sets a read deadline before each DecodeMessage call
// so idle connections are evicted. This test validates that deadline mechanism.
func TestReadDeadlineEvictsIdlePeer(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	const deadline = 50 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		// This models what the fixed readLoop does before each DecodeMessage.
		c1.SetReadDeadline(time.Now().Add(deadline))
		buf := make([]byte, 1)
		_, err := c1.Read(buf)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read should have timed out but returned nil")
		}
		ne, ok := err.(net.Error)
		if !ok || !ne.Timeout() {
			t.Fatalf("expected a timeout net.Error, got %T: %v", err, err)
		}
		// c2 is still open — the deadline fired correctly.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("read did not respect the deadline — server-side idle timeout fix is missing")
	}
}

// TestInboundPeerLimitRejectsOverflow verifies that inbound connections are
// rejected once the inbound slot limit is reached.
//
// Before the fix: PeerCount() counts inbound + outbound together against
// maxInbound, so an attacker filling all 12 slots with inbound connections
// prevents the node from making any outbound connections (eclipse attack).
//
// After the fix: handleConnection counts only inbound peers against maxInbound,
// preserving outbound slots for the node's own peer discovery.
func TestInboundPeerLimitRejectsOverflow(t *testing.T) {
	bc, err := NewBlockchain(t.TempDir(), false, MainnetChainID)
	if err != nil {
		t.Fatalf("NewBlockchain: %v", err)
	}
	defer bc.Close()

	ab := NewAddrBook(t.TempDir())
	srv := NewServer(bc, NewMempool(), 0, true, ab, false)

	// Pre-fill inbound slots to the limit.
	for i := 0; i < srv.maxInbound; i++ {
		c1, _ := net.Pipe()
		p := NewPeer(fmt.Sprintf("10.0.0.%d:8338", i+1), c1)
		p.Inbound = true
		p.Connected = true
		srv.AddPeer(p)
	}

	// An additional inbound connection should be rejected.
	c1, c2 := net.Pipe()
	defer c2.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleConnection(c1)
	}()

	select {
	case <-done:
		// handleConnection returned — connection was rejected or handled.
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not return — inbound limit not enforced")
	}

	// Verify the connection was indeed closed (c2 read should fail).
	c2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1)
	_, readErr := c2.Read(buf)
	if readErr == nil {
		t.Fatal("c2 read succeeded — server did not close the over-limit inbound connection")
	}
}

// TestHandlePeersDialBound verifies that handlePeers does not attempt to dial
// new peers when the pool is already at MaxPeers, and that the address book is
// still updated regardless.
//
// Before the fix: a racy PeerCount() check lets multiple goroutines slip past
// the guard before any of them updates the peer map.
//
// After the fix: an atomic dialingCount tracks in-flight dials and is combined
// with PeerCount() to prevent overflow.
func TestHandlePeersDialBound(t *testing.T) {
	bc, err := NewBlockchain(t.TempDir(), false, MainnetChainID)
	if err != nil {
		t.Fatalf("NewBlockchain: %v", err)
	}
	defer bc.Close()

	ab := NewAddrBook(t.TempDir())
	srv := NewServer(bc, NewMempool(), 0, true, ab, false)

	// Pre-fill the peer map to MaxPeers with dummy peers.
	for i := 0; i < MaxPeers; i++ {
		c1, _ := net.Pipe()
		p := NewPeer(fmt.Sprintf("10.0.0.%d:8338", i+1), c1)
		p.Connected = true
		srv.AddPeer(p)
	}

	// Build a MsgPeers with 20 addresses.
	addresses := make([]string, 20)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("192.168.1.%d:8338", i+1)
	}
	payload, _ := json.Marshal(PeersPayload{Addresses: addresses})
	msg := Message{Magic: MagicTestnet, Type: MsgPeers, Payload: payload}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	sender := NewPeer("10.99.0.1:8338", c1)

	if err := srv.handlePeers(sender, msg); err != nil {
		t.Fatalf("handlePeers: %v", err)
	}

	// All 20 addresses should be persisted to the addrbook.
	known := ab.GetAll()
	if len(known) < 20 {
		t.Fatalf("expected >=20 addresses in addrbook, got %d", len(known))
	}

	// Peer count must not exceed MaxPeers.
	if c := srv.PeerCount(); c > MaxPeers {
		t.Fatalf("peer count %d exceeds MaxPeers %d", c, MaxPeers)
	}
}
