package p2p

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"chakram/consensus"
)

const (
	maxSyncBatch   = 500
	maxBodyBytes   = 2 << 20 // 2 MB per POST /block body
	maxBlockPushes = 5       // POST /block requests per IP per second
	maxRequests    = 60      // all requests per IP per second
	banDuration    = 5 * time.Minute
)

// ReceiverFunc is called when a peer pushes a new block via POST /block.
type ReceiverFunc func(b *consensus.ConsensusBlock) error

// PoWValidator verifies a block's RandomX proof-of-work.
// It receives the header, must serialize it to 120 bytes, run RandomX, and
// check whether the output meets the declared difficulty target.
// Never use the wire hash (b.Hash) for PoW — always recompute from the header.
//
// Mandatory for non-genesis blocks: if no PoWValidator is configured the
// server rejects all height > 0 blocks (fail-closed).
type PoWValidator func(header *consensus.ConsensusBlockHeader) bool

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithReceiver sets the callback invoked on POST /block.
func WithReceiver(fn ReceiverFunc) ServerOption {
	return func(s *Server) { s.receiver = fn }
}

// WithPoWValidator sets the mandatory RandomX PoW validator.
// Without it, the server rejects every non-genesis block.
func WithPoWValidator(fn PoWValidator) ServerOption {
	return func(s *Server) { s.pow = fn }
}

// WithChainID sets the chain ID returned in GET /status and used for peer
// validation. Peers advertising a different chain ID are refused.
func WithChainID(id uint64) ServerOption {
	return func(s *Server) { s.chainID = id }
}

// WithGenesisHash sets the genesis block hash returned in GET /status.
// Must be a 0x-prefixed lowercase 64-char hex string (keccak256 of the EVM
// genesis block). Peers advertising a different genesis hash are refused.
func WithGenesisHash(h string) ServerOption {
	return func(s *Server) { s.genesisHash = h }
}

// ipState tracks per-IP rate-limit and ban state.
type ipState struct {
	reqCount    int
	pushCount   int
	windowStart time.Time
	bannedUntil time.Time
}

// Server is the HTTP server that exposes consensus blocks to peers.
type Server struct {
	store       BlockStore
	receiver    ReceiverFunc
	pow         PoWValidator
	chainID     uint64
	genesisHash string // 0x-prefixed lowercase hex
	mux         *http.ServeMux

	mu    sync.Mutex
	peers map[string]*ipState
}

// NewServer creates a Server backed by store. Options are applied in order.
func NewServer(store BlockStore, opts ...ServerOption) *Server {
	s := &Server{
		store: store,
		peers: make(map[string]*ipState),
	}
	for _, o := range opts {
		o(s)
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/status", s.handleStatus)
	s.mux.HandleFunc("/block", s.handleBlock)
	s.mux.HandleFunc("/blocks", s.handleBlocks)
	s.mux.HandleFunc("/locator", s.handleLocator)
	return s
}

// NewHTTPServer returns an *http.Server wrapping this handler with production timeouts.
func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      s,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// ServeHTTP implements http.Handler so Server can be used with httptest and http.Server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	isBlockPush := r.Method == http.MethodPost && r.URL.Path == "/block"
	if !s.allowRequest(ip, isBlockPush) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// allowRequest checks and updates the per-IP rate limit.
func (s *Server) allowRequest(ip string, isBlockPush bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	st := s.peers[ip]
	if st == nil {
		st = &ipState{windowStart: now}
		s.peers[ip] = st
	}

	if now.Before(st.bannedUntil) {
		return false
	}

	if now.Sub(st.windowStart) >= time.Second {
		st.reqCount = 0
		st.pushCount = 0
		st.windowStart = now
	}

	st.reqCount++
	if st.reqCount > maxRequests {
		return false
	}
	if isBlockPush {
		st.pushCount++
		if st.pushCount > maxBlockPushes {
			return false
		}
	}
	return true
}

// banIP bans the given IP for banDuration. Subsequent requests return 429.
func (s *Server) banIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.peers[ip]
	if st == nil {
		st = &ipState{}
		s.peers[ip] = st
	}
	st.bannedUntil = time.Now().Add(banDuration)
}

// remoteIP extracts the IP address (without port) from r.RemoteAddr.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tipHash, tipHeight, err := s.store.GetTip()
	if err != nil {
		http.Error(w, "no tip", http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, StatusResponse{
		Height:      tipHeight,
		TipHash:     consensus.HashToHex(tipHash),
		ChainID:     s.chainID,
		GenesisHash: s.genesisHash,
	})
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.serveBlock(w, r)
	case http.MethodPost:
		s.receiveBlock(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveBlock(w http.ResponseWriter, r *http.Request) {
	hashHex := r.URL.Query().Get("hash")
	if hashHex == "" {
		http.Error(w, "missing hash", http.StatusBadRequest)
		return
	}
	hash, err := consensus.HashFromHex(hashHex)
	if err != nil {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}
	b, err := s.store.GetBlock(hash)
	if err != nil {
		if errors.Is(err, consensus.ErrBlockNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.attachPayload(b)
	jsonOK(w, BlockToWire(b))
}

func (s *Server) receiveBlock(w http.ResponseWriter, r *http.Request) {
	// Limit body size before decoding to prevent memory exhaustion attacks.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var wire WireBlock
	if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	b, err := WireToBlock(wire)
	if err != nil {
		http.Error(w, "invalid block: "+err.Error(), http.StatusBadRequest)
		return
	}

	ip := remoteIP(r)

	// Check 1: recompute the block identifier (SHA-256²) and verify it matches
	// the wire hash. Pure SHA-256 — no RandomX. Prevents tampered headers from
	// reaching the receiver.
	if consensus.GenesisHash(&b.Header) != b.Hash {
		s.banIP(ip)
		http.Error(w, "hash mismatch", http.StatusBadRequest)
		return
	}

	// Check 2: RandomX proof-of-work (mandatory for every non-genesis block).
	// The validator serializes the 120-byte header, runs RandomX, and checks the
	// output against the declared difficulty target. We never use the wire hash
	// for PoW — it is the SHA-256² block identifier, not the RandomX output.
	//
	// Fail-closed: if no validator is configured, reject the block rather than
	// forwarding an unverified block to newPayloadV3.
	if b.Header.Height > 0 {
		if s.pow == nil {
			http.Error(w, "pow validator not configured", http.StatusServiceUnavailable)
			return
		}
		if !s.pow(&b.Header) {
			s.banIP(ip)
			http.Error(w, "invalid proof of work", http.StatusBadRequest)
			return
		}
	}

	if s.receiver != nil {
		if err := s.receiver(b); err != nil {
			http.Error(w, "rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fromStr := r.URL.Query().Get("from")
	limitStr := r.URL.Query().Get("limit")

	from, err := strconv.ParseUint(fromStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid from", http.StatusBadRequest)
		return
	}
	limit, err := strconv.ParseUint(limitStr, 10, 64)
	if err != nil || limit == 0 {
		limit = 100
	}
	if limit > maxSyncBatch {
		limit = maxSyncBatch
	}

	result := make([]WireBlock, 0, limit)
	for h := from; h < from+limit; h++ {
		b, err := s.store.GetBlockByHeight(h)
		if err != nil {
			break // reached end of known chain
		}
		s.attachPayload(b)
		result = append(result, BlockToWire(b))
	}
	jsonOK(w, result)
}

// attachPayload fetches the execution payload from the store and sets it on b.
// Logs and continues if not found (genesis has no payload).
func (s *Server) attachPayload(b *consensus.ConsensusBlock) {
	if len(b.ExecutionPayload) > 0 {
		return // already set (e.g. freshly mined block still in memory)
	}
	payload, err := s.store.GetPayload(b.Hash)
	if err == nil {
		b.ExecutionPayload = payload
	}
}

// handleLocator finds the newest common ancestor given a Bitcoin-style locator.
// The client sends an ordered list of (height, hash) pairs from its canonical
// chain; the server returns the height of the first entry it recognises.
func (s *Server) handleLocator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req LocatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	for _, entry := range req.Entries {
		hash, err := consensus.HashFromHex(entry.Hash)
		if err != nil {
			continue
		}
		b, err := s.store.GetBlockByHeight(entry.Height)
		if err != nil {
			continue
		}
		if b.Hash == hash {
			jsonOK(w, LocatorResponse{CommonHeight: entry.Height})
			return
		}
	}
	// No entry matched — report genesis as the fallback common point.
	jsonOK(w, LocatorResponse{CommonHeight: 0})
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
