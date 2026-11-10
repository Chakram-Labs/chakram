package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	var (
		listenAddr = flag.String("listen", "0.0.0.0:8081", "API listen address")
		dbPath     = flag.String("db", defaultDBPath(), "SQLite database path")
		gethURL    = flag.String("geth", "http://127.0.0.1:8545", "Geth JSON-RPC URL")
		cometURL   = flag.String("comet", "http://127.0.0.1:26657", "CometBFT RPC URL")
		solcPath   = flag.String("solc", "solc", "Path to solc binary for contract verification")
		adminKey   = flag.String("admin-key", "", "Secret key for admin endpoints (empty = disabled)")
		fromBlock  = flag.Int64("from-block", 1, "Start indexing from this block (first run only)")
		nodeAPI    = flag.String("node-api", "http://127.0.0.1:8080", "Chakram node API URL for staking profiles")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	db, err := openDB(*dbPath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	idx := &Indexer{
		db:        db,
		geth:      newGethClient(*gethURL),
		comet:     newCometClient(*cometURL),
		log:       log,
		fromBlock: *fromBlock,
	}
	go idx.Run(ctx)

	h := &Handler{
		db:         db,
		geth:       newGethClient(*gethURL),
		comet:      newCometClient(*cometURL),
		nodeAPIURL: *nodeAPI,
		solcPath:   *solcPath,
		adminKey:   *adminKey,
		log:        log,
	}

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      withCORS(h.routes()),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("chakram explorer API", "addr", *listenAddr, "db", *dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("explorer stopped")
}

func withCORS(next http.Handler) http.Handler {
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

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "explorer.db"
	}
	return home + "/.chakram-node/explorer.db"
}
