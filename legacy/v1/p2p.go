// p2p.go — Entire P2P networking layer for the Chakram node.
// All peer discovery, connections, and message routing lives here.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxPeerViolations = 5
	banDuration       = 24 * time.Hour  // how long a banned address stays banned
)

// ── Message types ─────────────────────────────────────────────────────────────

type MessageType uint8

const (
	MsgVersion   MessageType = 1
	MsgVerAck    MessageType = 2
	MsgGetBlocks MessageType = 3
	MsgInv       MessageType = 4
	MsgGetData   MessageType = 5
	MsgBlock     MessageType = 6
	MsgTx        MessageType = 7
	MsgPing      MessageType = 8
	MsgPong      MessageType = 9
	MsgGetPeers  MessageType = 10
	MsgPeers     MessageType = 11
)

const maxPayloadBytes uint32 = 32 * 1024 * 1024 // 32 MB hard cap

// ── Payload structs ───────────────────────────────────────────────────────────

type VersionPayload struct {
	Version    uint32 `json:"version"`
	Height     uint64 `json:"height"`
	UserAgent  string `json:"user_agent"`
	Timestamp  int64  `json:"timestamp"`
	Nonce      uint64 `json:"nonce"`
	ListenPort uint16 `json:"listen_port,omitempty"` // peer's actual listen port so inbound addresses are dialable
}

// InvItem announces a single block (Type=1) or transaction (Type=2) by hash.
type InvItem struct {
	Type uint8  `json:"type"`
	Hash []byte `json:"hash"`
}

type InvPayload struct {
	Items []InvItem `json:"items"`
}

type GetBlocksPayload struct {
	FromHeight uint64 `json:"from_height"`
	Count      uint32 `json:"count"` // capped at 500 in handler
}

type GetDataPayload struct {
	Type uint8  `json:"type"`
	Hash []byte `json:"hash"`
}

type PeersPayload struct {
	Addresses []string `json:"addresses"`
}

// ── Message ───────────────────────────────────────────────────────────────────

// Message is the envelope for every Chakram P2P message.
type Message struct {
	Magic   [4]byte
	Type    MessageType
	Length  uint32
	Payload []byte
}

// NewMessage JSON-encodes payload and wraps it in a Message with correct Length.
func NewMessage(magic [4]byte, msgType MessageType, payload interface{}) (Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("marshal payload: %w", err)
	}
	return Message{
		Magic:   magic,
		Type:    msgType,
		Length:  uint32(len(data)),
		Payload: data,
	}, nil
}

// Encode serialises the message to wire format:
// [magic 4B] [type 1B] [length 4B big-endian] [payload]
func (m Message) Encode() ([]byte, error) {
	out := make([]byte, 0, 9+len(m.Payload))
	out = append(out, m.Magic[:]...)
	out = append(out, byte(m.Type))
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], m.Length)
	out = append(out, lb[:]...)
	out = append(out, m.Payload...)
	return out, nil
}

// DecodeMessage reads one complete message from r.
// Rejects any message whose magic bytes do not match expectedMagic, preventing
// cross-network message injection (mainnet nodes reject testnet messages and
// vice versa). Caps payload size at 32 MB.
func DecodeMessage(r io.Reader, expectedMagic [4]byte) (Message, error) {
	var msg Message

	if _, err := io.ReadFull(r, msg.Magic[:]); err != nil {
		return msg, fmt.Errorf("read magic: %w", err)
	}
	if msg.Magic != expectedMagic {
		return msg, fmt.Errorf("network magic mismatch: got %x, want %x", msg.Magic, expectedMagic)
	}

	var typeBuf [1]byte
	if _, err := io.ReadFull(r, typeBuf[:]); err != nil {
		return msg, fmt.Errorf("read type: %w", err)
	}
	msg.Type = MessageType(typeBuf[0])

	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return msg, fmt.Errorf("read length: %w", err)
	}
	msg.Length = binary.BigEndian.Uint32(lenBuf[:])
	if msg.Length > maxPayloadBytes {
		return msg, fmt.Errorf("payload size %d exceeds 32 MB cap", msg.Length)
	}

	if msg.Length > 0 {
		msg.Payload = make([]byte, msg.Length)
		if _, err := io.ReadFull(r, msg.Payload); err != nil {
			return msg, fmt.Errorf("read payload: %w", err)
		}
	}
	return msg, nil
}

// ── Peer ──────────────────────────────────────────────────────────────────────

// Peer represents a remote Chakram node.
type Peer struct {
	Address     string
	Conn        net.Conn
	Height      uint64
	Version     uint32
	Connected   bool
	Inbound     bool        // true when the peer dialed us (vs. we dialed them)
	LastSeen    time.Time
	violations  int         // protocol violations; banned at maxPeerViolations
	send        chan Message // outbound message queue
	versionSent bool        // true once we have sent our version to this peer
}

// NewPeer creates a Peer with a 100-message outbound buffer.
func NewPeer(address string, conn net.Conn) *Peer {
	return &Peer{
		Address:  address,
		Conn:     conn,
		LastSeen: time.Now(),
		send:     make(chan Message, 100),
	}
}

// Send enqueues msg for delivery. Returns an error if the queue is full (slow peer).
func (p *Peer) Send(msg Message) error {
	select {
	case p.send <- msg:
		return nil
	default:
		return fmt.Errorf("peer %s: outbound queue full", p.Address)
	}
}

// writeLoop drains the send channel, encodes each message, and writes it to the connection.
// A per-write deadline prevents a slow/stalled peer from blocking the goroutine indefinitely.
// Exits when the channel is closed or a write error occurs.
func (p *Peer) writeLoop() {
	const writeTimeout = 30 * time.Second
	for msg := range p.send {
		data, err := msg.Encode()
		if err != nil {
			continue
		}
		p.Conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := p.Conn.Write(data); err != nil {
			return
		}
	}
}

// ── Server ────────────────────────────────────────────────────────────────────

// Server is the Chakram P2P node. It listens for incoming connections, manages
// peers, and routes messages to the appropriate handlers.
type Server struct {
	Blockchain   *Blockchain
	Mempool      *Mempool
	SyncManager  *SyncManager
	AddrBook     *AddrBook
	peers        map[string]*Peer
	banned         map[string]time.Time   // IP address → ban expiry
	bannedFile     string                 // path to banned.json for persistence across restarts
	mu             sync.RWMutex
	port           int
	magic          [4]byte
	quit           chan struct{}
	listener       net.Listener
	listenAddr     string // canonical listen address set in Start()
	nonce          uint64 // random nonce to detect self-connections
	maxInbound     int    // max inbound peer connections (higher for seed nodes)
	dialingCount   int32  // atomic count of in-flight outbound dials (prevents race in handlePeers)
	pendingInv     map[string]time.Time   // hashes we have sent GetData for, awaiting block
	pendingInvMu   sync.Mutex
	connAttempts   map[string][]time.Time // inbound connection timestamps per IP for rate limiting
	connAttemptsMu sync.Mutex
}

// SetSyncManager wires a SyncManager into the server after construction.
func (s *Server) SetSyncManager(sm *SyncManager) {
	s.SyncManager = sm
}

// NewServer creates a Server. Use testnet=true to select testnet magic bytes.
// seedMode raises the inbound connection limit to 125 for infrastructure seeds.
// Normal nodes reserve 4 outbound slots so they can always initiate peer discovery
// regardless of inbound pressure (eclipse-attack mitigation).
func NewServer(bc *Blockchain, mp *Mempool, port int, testnet bool, addrBook *AddrBook, seedMode bool) *Server {
	magic := MagicMainnet
	if testnet {
		magic = MagicTestnet
	}
	maxInbound := MaxPeers - 4 // reserve 4 slots for outbound connections
	if seedMode {
		maxInbound = 125
		fmt.Println("Seed mode: inbound peer limit raised to 125")
	}
	return &Server{
		Blockchain:   bc,
		Mempool:      mp,
		AddrBook:     addrBook,
		peers:        make(map[string]*Peer),
		banned:       make(map[string]time.Time),
		bannedFile:   filepath.Join(addrBook.DataDir(), "banned.json"),
		port:         port,
		magic:        magic,
		quit:         make(chan struct{}),
		nonce:        rand.Uint64(),
		maxInbound:   maxInbound,
		pendingInv:   make(map[string]time.Time),
		connAttempts: make(map[string][]time.Time),
	}
}

// isBanned reports whether addr is currently banned.
func (s *Server) isBanned(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	s.mu.RLock()
	expiry, ok := s.banned[host]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		s.mu.Lock()
		delete(s.banned, host)
		s.mu.Unlock()
		return false
	}
	return true
}

// banPeer immediately disconnects peer and bans its IP for banDuration.
func (s *Server) banPeer(p *Peer) {
	host, _, err := net.SplitHostPort(p.Address)
	if err != nil {
		host = p.Address
	}
	s.mu.Lock()
	s.banned[host] = time.Now().Add(banDuration)
	s.mu.Unlock()
	fmt.Printf("Peer %s banned for %s\n", p.Address, banDuration)
	s.saveBans()
	s.RemovePeer(p)
}

// penalizePeer increments a peer's violation counter and bans it if the
// threshold is reached. Returns true when the peer was banned.
func (s *Server) penalizePeer(p *Peer) bool {
	p.violations++
	if p.violations >= maxPeerViolations {
		s.banPeer(p)
		return true
	}
	return false
}

// isConnRateLimited returns true if the IP has made more than 5 inbound
// connection attempts in the last 60 seconds. Self-cleaning: stale entries are
// pruned on every call so the map never grows unboundedly.
func (s *Server) isConnRateLimited(ip string) bool {
	s.connAttemptsMu.Lock()
	defer s.connAttemptsMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)
	prev := s.connAttempts[ip]
	filtered := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, now)
	s.connAttempts[ip] = filtered
	return len(filtered) > 5
}

// saveBans persists the current ban list to bannedFile so bans survive restarts.
func (s *Server) saveBans() {
	s.mu.RLock()
	data, err := json.Marshal(s.banned)
	s.mu.RUnlock()
	if err != nil {
		return
	}
	os.WriteFile(s.bannedFile, data, 0600) //nolint:errcheck — best-effort persist
}

// loadBans reads bannedFile and restores non-expired bans into s.banned.
func (s *Server) loadBans() {
	data, err := os.ReadFile(s.bannedFile)
	if err != nil {
		return // file doesn't exist yet — fresh start
	}
	var m map[string]time.Time
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	for ip, expiry := range m {
		if expiry.After(now) {
			s.banned[ip] = expiry
		}
	}
	s.mu.Unlock()
}

// isOwnAddress returns true if addr is one of our own listening addresses.
// Checks both an exact match against listenAddr and whether the addr's host
// is a local network interface with our listen port.
func (s *Server) isOwnAddress(addr string) bool {
	if addr == s.listenAddr {
		return true
	}
	seedHost, seedPort, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if seedPort != fmt.Sprintf("%d", s.port) {
		return false
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		var ip net.IP
		if ipnet, ok := iface.(*net.IPNet); ok {
			ip = ipnet.IP
		} else if ipaddr, ok := iface.(*net.IPAddr); ok {
			ip = ipaddr.IP
		}
		if ip != nil && ip.String() == seedHost {
			return true
		}
	}
	return false
}

// Start opens a TCP listener and spawns the accept and ping loops.
func (s *Server) Start() error {
	s.loadBans()

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", s.port))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", s.port, err)
	}
	s.listenAddr = fmt.Sprintf("0.0.0.0:%d", s.port)
	s.listener = ln

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.quit:
					return
				default:
					continue
				}
			}
			go s.handleConnection(conn)
		}
	}()

	go s.pingLoop()
	return nil
}

// Stop signals all goroutines to exit, closes peer connections, and shuts the listener.
// Peer connections are closed concurrently and the call returns within 3 seconds.
func (s *Server) Stop() {
	close(s.quit)
	if s.listener != nil {
		s.listener.Close()
	}

	// Snapshot and clear the peers map atomically so RemovePeer can't race us.
	s.mu.Lock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.peers = make(map[string]*Peer)
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, p := range peers {
			wg.Add(1)
			go func(peer *Peer) {
				defer wg.Done()
				close(peer.send)
				peer.Conn.Close()
			}(p)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// ConnectToPeer dials an outbound connection, sends our version, and starts loops.
func (s *Server) ConnectToPeer(address string) error {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", address, err)
	}
	peer := NewPeer(address, conn)
	s.AddPeer(peer)
	go peer.writeLoop()
	go s.readLoop(peer)
	return s.sendVersion(peer)
}

// handleConnection manages a full inbound connection lifecycle.
// Checks the inbound-only slot counter so outbound discovery slots are never
// consumed by inbound connections (eclipse-attack mitigation).
func (s *Server) handleConnection(conn net.Conn) {
	addr := conn.RemoteAddr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if s.isBanned(addr) {
		conn.Close()
		return
	}
	if s.isConnRateLimited(host) {
		conn.Close()
		return
	}
	if s.inboundCount() >= s.maxInbound {
		conn.Close()
		return
	}
	peer := NewPeer(addr, conn)
	peer.Inbound = true
	s.AddPeer(peer)
	go peer.writeLoop()
	if err := s.sendVersion(peer); err != nil {
		s.RemovePeer(peer)
		return
	}
	s.readLoop(peer)
}

// readIdleTimeout is the maximum time a peer may be silent before we drop it.
// The ping loop evicts peers not seen in 90 s; this deadline provides a lower-
// level guarantee that a silent connection cannot hold a goroutine forever.
const readIdleTimeout = 2 * time.Minute

// readLoop reads messages from peer until the connection drops or quit is signalled.
// A read deadline is set before every DecodeMessage call so silent peers are
// evicted within readIdleTimeout regardless of ping-loop timing.
// Protocol violations increment the peer's violation counter; at the threshold
// the peer is banned.
func (s *Server) readLoop(peer *Peer) {
	defer s.RemovePeer(peer)
	for {
		select {
		case <-s.quit:
			return
		default:
		}
		peer.Conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
		msg, err := DecodeMessage(peer.Conn, s.magic)
		if err != nil {
			if !peer.Connected {
				fmt.Printf("[P2P] peer %s dropped before handshake: %v\n", peer.Address, err)
			}
			return
		}
		peer.LastSeen = time.Now()
		if err := s.handleMessage(peer, msg); err != nil {
			// Only penalize after a successful handshake. Pre-handshake errors
			// (e.g. from reconnect storms) are not deliberate protocol violations.
			if peer.Connected {
				if s.penalizePeer(peer) {
					return
				}
			}
		}
	}
}

// sendVersion sends our node's current version and chain height to peer.
func (s *Server) sendVersion(peer *Peer) error {
	vp := VersionPayload{
		Version:    ProtocolVersion,
		Height:     s.Blockchain.GetHeight(),
		UserAgent:  fmt.Sprintf("Chakram/%s (protocol %d)", SoftwareVersion, ProtocolVersion),
		Timestamp:  time.Now().Unix(),
		Nonce:      s.nonce,
		ListenPort: uint16(s.port),
	}
	msg, err := NewMessage(s.magic, MsgVersion, vp)
	if err != nil {
		return err
	}
	peer.versionSent = true
	return peer.Send(msg)
}

// handleMessage dispatches an incoming message to its handler.
func (s *Server) handleMessage(peer *Peer, msg Message) error {
	switch msg.Type {
	case MsgVersion:
		return s.handleVersion(peer, msg)
	case MsgVerAck:
		return s.handleVerAck(peer, msg)
	case MsgGetBlocks:
		return s.handleGetBlocks(peer, msg)
	case MsgInv:
		return s.handleInv(peer, msg)
	case MsgGetData:
		return s.handleGetData(peer, msg)
	case MsgBlock:
		return s.handleBlock(peer, msg)
	case MsgTx:
		return s.handleTx(peer, msg)
	case MsgPing:
		return s.handlePing(peer)
	case MsgPong:
		peer.LastSeen = time.Now()
	case MsgGetPeers:
		return s.handleGetPeers(peer)
	case MsgPeers:
		return s.handlePeers(peer, msg)
	}
	return nil
}

// ── Message handlers ──────────────────────────────────────────────────────────

func (s *Server) handleVersion(peer *Peer, msg Message) error {
	var vp VersionPayload
	if err := json.Unmarshal(msg.Payload, &vp); err != nil {
		return fmt.Errorf("decode version: %w", err)
	}
	if vp.Nonce == s.nonce {
		fmt.Printf("[P2P] disconnecting self-connection from %s\n", peer.Address)
		return fmt.Errorf("self-connection detected")
	}
	peer.Height = vp.Height
	peer.Version = vp.Version

	// If the peer told us their listen port, rewrite peer.Address to use it.
	// For inbound connections peer.Address is the ephemeral remote port, which
	// is useless for reconnecting. The listen port is the dialable address.
	if vp.ListenPort > 0 {
		if host, _, err := net.SplitHostPort(peer.Address); err == nil {
			canonical := fmt.Sprintf("%s:%d", host, vp.ListenPort)
			// Re-register under the canonical address if it changed.
			if canonical != peer.Address {
				s.mu.Lock()
				delete(s.peers, peer.Address)
				peer.Address = canonical
				s.peers[canonical] = peer
				s.mu.Unlock()
			}
		}
	}

	if vp.Version < MinProtocolVersion {
		return fmt.Errorf("peer %s: protocol version %d below minimum %d — upgrade required",
			peer.Address, vp.Version, MinProtocolVersion)
	}

	// Send our version back if we haven't yet — ensures the peer always learns
	// our current height even when they initiated the connection.
	if !peer.versionSent {
		if err := s.sendVersion(peer); err != nil {
			return err
		}
	}

	ack, err := NewMessage(s.magic, MsgVerAck, struct{}{})
	if err != nil {
		return err
	}
	return peer.Send(ack)
}

func (s *Server) handleVerAck(peer *Peer, msg Message) error {
	peer.Connected = true

	// Record this peer in the address book so we remember it across restarts.
	if s.AddrBook != nil {
		s.AddrBook.Add(peer.Address)
		s.AddrBook.Save()
	}

	// Ask every new peer for their peer list so we discover the full network,
	// not just the seeds we hardcoded.
	if gp, err := NewMessage(s.magic, MsgGetPeers, struct{}{}); err == nil {
		peer.Send(gp) //nolint:errcheck — best-effort discovery
	}

	if s.SyncManager != nil {
		s.SyncManager.OnPeerConnected(peer)
		return nil
	}
	// Fallback (no SyncManager): request blocks if peer is ahead.
	if peer.Height > s.Blockchain.GetHeight() {
		req, err := NewMessage(s.magic, MsgGetBlocks, GetBlocksPayload{
			FromHeight: s.Blockchain.GetHeight(),
			Count:      500,
		})
		if err != nil {
			return err
		}
		return peer.Send(req)
	}
	return nil
}

func (s *Server) handleGetBlocks(peer *Peer, msg Message) error {
	var gp GetBlocksPayload
	if err := json.Unmarshal(msg.Payload, &gp); err != nil {
		return fmt.Errorf("decode getblocks: %w", err)
	}
	count := gp.Count
	if count > 500 {
		count = 500
	}

	ourHeight := s.Blockchain.GetHeight()
	var items []InvItem
	for h := gp.FromHeight + 1; h <= ourHeight && uint32(len(items)) < count; h++ {
		b, err := s.Blockchain.GetBlock(h)
		if err != nil {
			break
		}
		items = append(items, InvItem{Type: 1, Hash: b.Hash})
	}
	if len(items) == 0 {
		return nil
	}
	inv, err := NewMessage(s.magic, MsgInv, InvPayload{Items: items})
	if err != nil {
		return err
	}
	return peer.Send(inv)
}

func (s *Server) handleInv(peer *Peer, msg Message) error {
	var inv InvPayload
	if err := json.Unmarshal(msg.Payload, &inv); err != nil {
		return fmt.Errorf("decode inv: %w", err)
	}
	for _, item := range inv.Items {
		switch item.Type {
		case 1: // block
			if !s.Blockchain.IsBlockOnMainChain(item.Hash) {
				hashStr := fmt.Sprintf("%x", item.Hash)
				s.pendingInvMu.Lock()
				if _, exists := s.pendingInv[hashStr]; exists {
					s.pendingInvMu.Unlock()
					continue // already sent GetData for this hash, ignore duplicate
				}
				if len(s.pendingInv) >= 10_000 {
					s.pendingInvMu.Unlock()
					continue // cap reached — drop until GC catches up
				}
				s.pendingInv[hashStr] = time.Now()
				s.pendingInvMu.Unlock()
				req, err := NewMessage(s.magic, MsgGetData, GetDataPayload{Type: 1, Hash: item.Hash})
				if err == nil {
					peer.Send(req) //nolint:errcheck
				}
			}
		case 2: // transaction
			if _, err := s.Mempool.Get(item.Hash); err != nil {
				req, err := NewMessage(s.magic, MsgGetData, GetDataPayload{Type: 2, Hash: item.Hash})
				if err == nil {
					peer.Send(req) //nolint:errcheck
				}
			}
		}
	}
	return nil
}

func (s *Server) handleGetData(peer *Peer, msg Message) error {
	var gd GetDataPayload
	if err := json.Unmarshal(msg.Payload, &gd); err != nil {
		return fmt.Errorf("decode getdata: %w", err)
	}
	switch gd.Type {
	case 1: // block
		b, err := s.Blockchain.Storage.GetBlockByHash(gd.Hash)
		if err != nil {
			return nil // don't have it
		}
		resp, err := NewMessage(s.magic, MsgBlock, b)
		if err != nil {
			return err
		}
		return peer.Send(resp)
	case 2: // transaction
		tx, err := s.Mempool.Get(gd.Hash)
		if err != nil {
			return nil
		}
		resp, err := NewMessage(s.magic, MsgTx, tx)
		if err != nil {
			return err
		}
		return peer.Send(resp)
	}
	return nil
}

func (s *Server) handleBlock(peer *Peer, msg Message) error {
	var b Block
	if err := json.Unmarshal(msg.Payload, &b); err != nil {
		return fmt.Errorf("decode block: %w", err)
	}
	// Snapshot before processing so we can detect whether this block was new.
	alreadyHad := s.Blockchain.HasBlock(b.Hash)
	if s.SyncManager != nil {
		s.SyncManager.OnBlockReceived(&b, peer)
	} else {
		if err := s.Blockchain.AddBlock(&b); err != nil {
			return err
		}
	}
	// Relay the full block directly to all other peers when it was new to us.
	// Direct MsgBlock relay is unconditional — no inv→getdata round-trip, no
	// pendingInv dedup races, no timing windows where the sender might not yet
	// have the block indexed for a GetData response.
	if !alreadyHad && s.Blockchain.HasBlock(b.Hash) {
		s.Broadcast(msg, peer)
	}
	return nil
}

func (s *Server) handleTx(peer *Peer, msg Message) error {
	var tx Transaction
	if err := json.Unmarshal(msg.Payload, &tx); err != nil {
		return fmt.Errorf("decode tx: %w", err)
	}
	// Recompute canonical TxID so the relay inv uses the authoritative hash.
	// Mempool.Add also recomputes, but setting it here keeps the inv consistent.
	tx.TxID = tx.ComputeTxID()
	if err := s.Mempool.Add(&tx); err != nil {
		// Clearly invalid transactions (bad signature, malformed structure,
		// outpoint already claimed in pool) are protocol violations → penalize.
		// Benign rejections (already confirmed, pool full, UTXO not yet seen)
		// are not — return nil so the peer is not blamed.
		if errors.Is(err, ErrTxInvalid) || errors.Is(err, ErrTxDuplicateInput) || errors.Is(err, ErrBadSignature) {
			return err
		}
		return nil
	}
	inv, err := NewMessage(s.magic, MsgInv, InvPayload{
		Items: []InvItem{{Type: 2, Hash: tx.TxID}},
	})
	if err != nil {
		return err
	}
	s.Broadcast(inv, peer)
	return nil
}

func (s *Server) handlePing(peer *Peer) error {
	pong, err := NewMessage(s.magic, MsgPong, struct{}{})
	if err != nil {
		return err
	}
	return peer.Send(pong)
}

func (s *Server) handleGetPeers(peer *Peer) error {
	// Return all known addresses from the address book (not just currently
	// connected peers) so the requesting node gets the broadest possible view.
	var addrs []string
	if s.AddrBook != nil {
		addrs = s.AddrBook.GetAll()
	} else {
		s.mu.RLock()
		addrs = make([]string, 0, len(s.peers))
		for addr := range s.peers {
			addrs = append(addrs, addr)
		}
		s.mu.RUnlock()
	}
	// Cap response at 50 addresses to keep message size reasonable.
	if len(addrs) > 50 {
		addrs = addrs[:50]
	}
	resp, err := NewMessage(s.magic, MsgPeers, PeersPayload{Addresses: addrs})
	if err != nil {
		return err
	}
	return peer.Send(resp)
}

// maxDialsPerPeersMsg caps the number of outbound dials spawned from a single
// MsgPeers message so a malicious peer cannot flood us with addresses and
// exhaust the dial goroutine budget.
const maxDialsPerPeersMsg = 8

func (s *Server) handlePeers(peer *Peer, msg Message) error {
	var pp PeersPayload
	if err := json.Unmarshal(msg.Payload, &pp); err != nil {
		return fmt.Errorf("decode peers: %w", err)
	}
	var newDials int
	for _, addr := range pp.Addresses {
		if s.isOwnAddress(addr) || s.isBanned(addr) {
			continue
		}
		// Always persist to address book regardless of current peer count.
		if s.AddrBook != nil {
			s.AddrBook.Add(addr) // batched — Save() called once after the loop
		}
		// Combine established peers with in-flight dials for an accurate slot count.
		// Without dialingCount the racy PeerCount() check allowed multiple goroutines
		// to slip past the guard before any of them updated the peers map.
		if s.PeerCount()+int(atomic.LoadInt32(&s.dialingCount)) >= MaxPeers {
			continue
		}
		if newDials >= maxDialsPerPeersMsg {
			continue
		}
		s.mu.RLock()
		_, exists := s.peers[addr]
		s.mu.RUnlock()
		if !exists {
			newDials++
			atomic.AddInt32(&s.dialingCount, 1)
			go func(a string) {
				defer atomic.AddInt32(&s.dialingCount, -1)
				s.ConnectToPeer(a) //nolint:errcheck — best-effort
			}(addr)
		}
	}
	// Flush all new addresses to disk in one write instead of one per address.
	if s.AddrBook != nil {
		s.AddrBook.Save()
	}
	return nil
}

// ── Broadcast and peer management ─────────────────────────────────────────────

// Broadcast sends msg to all connected peers except exclude (may be nil).
// Snapshot the target list under RLock, then release before sending so the
// lock is not held during channel operations.
func (s *Server) Broadcast(msg Message, exclude *Peer) {
	s.mu.RLock()
	targets := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if !p.Connected {
			continue
		}
		if exclude != nil && p.Address == exclude.Address {
			continue
		}
		targets = append(targets, p)
	}
	s.mu.RUnlock()
	for _, p := range targets {
		p.Send(msg) //nolint:errcheck
	}
}

// AddPeer registers a peer under its address.
func (s *Server) AddPeer(p *Peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[p.Address] = p
}

// RemovePeer unregisters and closes a peer's connection.
// s.mu must be released BEFORE calling OnPeerDisconnected — that callback calls
// ConnectedPeers() which acquires s.mu.RLock(), and Go's RWMutex is not
// reentrant: holding Lock() and then calling RLock() in the same goroutine
// deadlocks permanently.
func (s *Server) RemovePeer(p *Peer) {
	s.mu.Lock()
	// Only delete if the map still points to THIS peer object.
	// A reconnect may have already replaced the entry with a new peer;
	// deleting by address key in that case would evict the healthy new connection.
	if current, exists := s.peers[p.Address]; exists && current == p {
		delete(s.peers, p.Address)
	}
	s.mu.Unlock()
	p.Conn.Close()
	if s.SyncManager != nil {
		s.SyncManager.OnPeerDisconnected(p)
	}
}

// PeerCount returns the total number of registered peers.
func (s *Server) PeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}

// IsConnected reports whether a peer with the given address has completed the handshake.
func (s *Server) IsConnected(address string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, exists := s.peers[address]
	return exists && p.Connected
}

// HasPeer reports whether a peer with the given address has been dialed
// (regardless of whether the handshake has completed yet).
func (s *Server) HasPeer(address string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.peers[address]
	return exists
}

// inboundCount returns the number of currently-registered inbound peers.
// Used by handleConnection to enforce the per-direction slot limit.
func (s *Server) inboundCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, p := range s.peers {
		if p.Inbound {
			n++
		}
	}
	return n
}

// ConnectedPeers returns a snapshot of all peers that have completed the handshake.
func (s *Server) ConnectedPeers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p.Connected {
			out = append(out, p)
		}
	}
	return out
}

// ── Ping loop ─────────────────────────────────────────────────────────────────

// pingLoop sends a MsgPing to every peer every 30 seconds.
// Peers not seen within 90 seconds (3 missed pings) are disconnected.
func (s *Server) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-ticker.C:
			ping, err := NewMessage(s.magic, MsgPing, struct{}{})
			if err != nil {
				continue
			}
			cutoff := time.Now().Add(-90 * time.Second)

			s.mu.RLock()
			var stale []*Peer
			for _, p := range s.peers {
				if p.LastSeen.Before(cutoff) {
					stale = append(stale, p)
				} else {
					p.Send(ping) //nolint:errcheck
				}
			}
			s.mu.RUnlock()

			for _, p := range stale {
				s.RemovePeer(p)
			}

			// Evict pendingInv entries older than 30 s so a missed block can be re-requested.
			invCutoff := time.Now().Add(-30 * time.Second)
			s.pendingInvMu.Lock()
			for k, t := range s.pendingInv {
				if t.Before(invCutoff) {
					delete(s.pendingInv, k)
				}
			}
			s.pendingInvMu.Unlock()
		}
	}
}
