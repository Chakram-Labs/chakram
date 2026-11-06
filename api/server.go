// Package api implements the Chakram Node API HTTP server.
// It proxies CometBFT RPC and geth JSON-RPC into a clean REST API,
// exposes Prometheus metrics, and serves the dashboard static files.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config holds Node API server configuration.
type Config struct {
	// ListenAddr is the TCP address to bind, e.g. "0.0.0.0:8080".
	ListenAddr string
	// CometRPCURL is the CometBFT RPC endpoint, e.g. "http://127.0.0.1:26657".
	CometRPCURL string
	// GethRPCURL is the geth HTTP-RPC endpoint, e.g. "http://127.0.0.1:8545".
	GethRPCURL string
	// DevMode enables the faucet endpoint.
	DevMode bool
	// FaucetAddr is the 0x-prefixed EVM address to send from (dev only).
	FaucetAddr string
	// FaucetPrivKey is the hex private key for FaucetAddr (dev only).
	FaucetPrivKey string
	// LogFile is the path to the chakram log file for SSE streaming.
	LogFile string
	// StaticDir is the path to the built dashboard static files (optional).
	StaticDir string
	// StaticFS is an embedded filesystem for the dashboard (takes priority over StaticDir).
	StaticFS fs.FS
	// RewardAddress is the EVM address this node uses for block rewards.
	RewardAddress string
	// StakingContractAddr is the deployed StakingRegistry contract address (empty = PoA mode).
	StakingContractAddr string
	// HomeDir is the CometBFT home directory (for serving genesis files).
	HomeDir string
	// P2PHost is the publicly reachable hostname/IP for P2P connections.
	P2PHost string
}

// Server is the Node API HTTP server.
type Server struct {
	cfg     Config
	srv     *http.Server
	reg     *prometheus.Registry
	metrics *Metrics
	log     *slog.Logger
	rl      *rateLimiter  // faucet: 1 req/min per IP
	apiRL   *burstLimiter // api: 100 req/10s per IP
}

// New creates a new Node API Server.
func New(cfg Config, log *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	m := newMetrics(reg)
	s := &Server{
		cfg:     cfg,
		reg:     reg,
		metrics: m,
		log:     log,
		rl:      newRateLimiter(faucetRateWindow),
		apiRL:   newBurstLimiter(10*time.Second, 100),
	}

	mux := http.NewServeMux()

	// Metrics (no rate limit — scraped internally)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// API routes — rate limited to 100 req/10s per IP
	mux.Handle("/api/status", s.apiRoute("/api/status", s.handleStatus))
	mux.Handle("/api/validator", s.apiRoute("/api/validator", s.handleValidator))
	mux.Handle("/api/blocks", s.apiRoute("/api/blocks", s.handleBlocks))
	mux.Handle("/api/block/", s.apiRoute("/api/block/", s.handleBlock))
	mux.Handle("/api/tx/", s.apiRoute("/api/tx/", s.handleTx))
	mux.Handle("/api/address/", s.apiRoute("/api/address/", s.handleAddress))
	mux.Handle("/api/peers", s.apiRoute("/api/peers", s.handlePeers))
	mux.Handle("/api/logs", s.apiRoute("/api/logs", s.handleLogs))
	mux.Handle("/api/staking", s.apiRoute("/api/staking", s.handleStaking))
	mux.Handle("/api/genesis", s.apiRoute("/api/genesis", s.handleGenesis))
	mux.Handle("/api/node-info", s.apiRoute("/api/node-info", s.handleNodeInfo))

	if cfg.DevMode {
		mux.Handle("/api/faucet", s.instrument("/api/faucet", http.HandlerFunc(s.handleFaucet)))
	}

	// OpenAPI spec
	mux.HandleFunc("/api/openapi.json", serveOpenAPI)

	// Dashboard static files — serve index.html for all non-file routes (SPA fallback).
	// Embedded FS takes priority; falls back to directory; falls back to JSON response.
	switch {
	case cfg.StaticFS != nil:
		fsrv := http.FileServer(http.FS(cfg.StaticFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if _, err := fs.Stat(cfg.StaticFS, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				// SPA fallback
				f, err2 := cfg.StaticFS.Open("index.html")
				if err2 != nil {
					http.NotFound(w, r)
					return
				}
				f.Close()
				data, _ := fs.ReadFile(cfg.StaticFS, "index.html")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(data)
				return
			}
			fsrv.ServeHTTP(w, r)
		})
	case cfg.StaticDir != "":
		fsrv := http.FileServer(http.Dir(cfg.StaticDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			path := filepath.Join(cfg.StaticDir, r.URL.Path)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				http.ServeFile(w, r, filepath.Join(cfg.StaticDir, "index.html"))
				return
			}
			fsrv.ServeHTTP(w, r)
		})
	default:
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				fmt.Fprint(w, `{"service":"chakram-node-api","docs":"/api/openapi.json"}`)
			} else {
				http.NotFound(w, r)
			}
		})
	}

	s.srv = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	return s
}

// Start starts the HTTP server. It returns as soon as listening begins.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("node api listen: %w", err)
	}
	s.log.Info("node api listening", "addr", s.cfg.ListenAddr, "dev_mode", s.cfg.DevMode)
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("node api serve error", "err", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// apiRoute combines instrumentation and per-IP burst rate limiting for API endpoints.
func (s *Server) apiRoute(path string, fn http.HandlerFunc) http.Handler {
	return s.instrument(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.apiRL.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limited: too many requests")
			return
		}
		fn(w, r)
	}))
}

// instrument wraps a handler to record request counts and update chain metrics.
func (s *Server) instrument(path string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(rw, r)
		status := fmt.Sprintf("%d", rw.status)
		s.metrics.APIRequests.WithLabelValues(path, status).Inc()
	})
}

// statusWriter wraps ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	spec := openAPISpec()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(spec)
}

// cometGet performs a GET request to the CometBFT RPC and decodes the JSON result.
func (s *Server) cometGet(path string, out any) error {
	url := s.cfg.CometRPCURL + path
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("comet rpc %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("comet rpc read: %w", err)
	}
	// Unwrap JSON-RPC envelope: {"jsonrpc":"2.0","id":-1,"result":{...}}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("comet rpc decode: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("comet rpc error: %s", envelope.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

// gethRPC calls a geth JSON-RPC method.
func (s *Server) gethRPC(method string, params []any, out any) error {
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	})
	resp, err := http.Post(s.cfg.GethRPCURL, "application/json", //nolint:noctx
		bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("geth rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("geth rpc decode %s: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("geth rpc %s error: %s", method, envelope.Error.Message)
	}
	if out != nil && envelope.Result != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

// tailFile reads the last N bytes of a file for log streaming.
func tailFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size > maxBytes {
		if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}
