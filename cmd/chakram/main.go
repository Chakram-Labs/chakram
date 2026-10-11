// cmd/chakram is the Chakram 2.0 consensus-layer node.
//
// Usage:
//
//	# one-time setup (devnet example)
//	geth init --datadir <datadir> config/genesis.json
//	openssl rand -hex 32 > <datadir>/jwt.hex
//
//	# start geth (terminal 1)
//	# --syncmode full is required: geth's default snap-sync startup mode returns
//	# SYNCING for newPayloadV3 until it has a finalized anchor, which a fresh
//	# single-node devnet never provides.
//	geth --datadir <datadir> \
//	  --syncmode full \
//	  --authrpc.addr 127.0.0.1 --authrpc.port 8551 \
//	  --authrpc.jwtsecret <datadir>/jwt.hex \
//	  --http --http.addr 127.0.0.1 --http.port 8545 --http.api eth,net,web3 \
//	  --nodiscover --maxpeers 0
//
//	# start consensus node (terminal 2)
//	./chakram --network devnet --datadir <datadir> --jwt <datadir>/jwt.hex --miner <0x…address>
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"chakram/cmd/chakram/p2p"
	"chakram/consensus"
	"chakram/engine"
)

// engineAPI is the subset of the geth Engine API used by the consensus client.
// *engine.Client satisfies this interface; tests substitute a mock.
type engineAPI interface {
	NewPayloadV3(ctx context.Context, payload engine.ExecutionPayloadV3, blobVersionedHashes []engine.Hash, parentBeaconBlockRoot engine.Hash) (engine.PayloadStatusV1, error)
	ForkchoiceUpdatedV3(ctx context.Context, state engine.ForkchoiceStateV1, attrs *engine.PayloadAttributesV3) (engine.ForkchoiceUpdatedResult, error)
	GetPayloadV3(ctx context.Context, payloadID string) (engine.GetPayloadResult, error)
}

// networkCfg holds the per-network parameters selected by --network.
type networkCfg struct {
	name           string
	chainID        uint64
	genesisHashHex string
	genesisBlock   func() *consensus.ConsensusBlock
	initialTarget  uint64
	defaultDataDir string
}

// networks is the authoritative mapping from --network value to config.
var networks = map[string]networkCfg{
	"devnet": {
		name:           "devnet",
		chainID:        consensus.DevnetChainID,
		genesisHashHex: consensus.GenesisExecutionPayloadHashHex,
		genesisBlock:   consensus.NewGenesisConsensusBlock,
		initialTarget:  consensus.DevnetInitialCompactTarget,
		defaultDataDir: "devnet-data",
	},
	"testnet": {
		name:           "testnet",
		chainID:        consensus.TestnetChainID,
		genesisHashHex: consensus.TestnetGenesisExecutionPayloadHashHex,
		genesisBlock:   consensus.NewTestnetGenesisConsensusBlock,
		initialTarget:  consensus.TestnetInitialCompactTarget,
		defaultDataDir: "testnet-data",
	},
	"mainnet": {
		name:           "mainnet",
		chainID:        consensus.MainnetChainID,
		genesisHashHex: consensus.MainnetGenesisExecutionPayloadHashHex,
		genesisBlock:   consensus.NewMainnetGenesisConsensusBlock,
		initialTarget:  consensus.InitialCompactTargetV2,
		defaultDataDir: "mainnet-data",
	},
}

func main() {
	var (
		networkName     = flag.String("network", "devnet", "network to connect to: devnet, testnet, mainnet")
		dataDir         = flag.String("datadir", "", "data directory for consensus blocks (default: <network>-data)")
		authrpc         = flag.String("authrpc", "http://127.0.0.1:8551", "geth Engine API (authrpc) URL")
		rpc             = flag.String("rpc", "http://127.0.0.1:8545", "geth plain JSON-RPC URL")
		jwt             = flag.String("jwt", "", "path to JWT secret file (required)")
		minerHex        = flag.String("miner", "0x0000000000000000000000000000000000000000", "miner Ethereum address (receives rewards)")
		listen          = flag.String("listen", "", "consensus P2P listen address (e.g. :9000); empty to disable")
		peersFlag       = flag.String("peers", "", "comma-separated consensus peer URLs (e.g. http://127.0.0.1:9001)")
		noDiscover      = flag.Bool("no-discover", false, "disable seed node lookup (for private networks)")
		noMine          = flag.Bool("no-mine", false, "run as a non-mining relay/sync node")
		light           = flag.Bool("light", false, "use RandomX light mode (~256 MB vs ~2 GB; for multi-node devnet on one machine)")
		threads         = flag.Int("threads", 0, "mining thread count (0 = NumCPU-1)")
		allowSlowMining = flag.Bool("allow-slow-mining", false, "allow mining with the pure-Go RandomX fallback (~1 H/s; for testing only)")
		// devnet-only: override genesis compact target on a fresh DB.
		// Disabled for testnet/mainnet where the target is baked into genesis.
		initialTargetS = flag.String("initial-target", "", "devnet only: override genesis compact target (hex); only applies to a fresh DB")
	)
	flag.Parse()
	_ = noDiscover // reserved for Phase 4 seed node implementation

	net, ok := networks[*networkName]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown --network %q (valid: devnet, testnet, mainnet)\n", *networkName)
		os.Exit(1)
	}

	dir := *dataDir
	if dir == "" {
		dir = net.defaultDataDir
	}

	if *jwt == "" {
		fmt.Fprintln(os.Stderr, "error: --jwt <path> is required")
		flag.Usage()
		os.Exit(1)
	}

	initialTarget := net.initialTarget
	if *initialTargetS != "" {
		if net.name != "devnet" {
			fmt.Fprintf(os.Stderr, "error: --initial-target may only be used with --network devnet\n")
			os.Exit(1)
		}
		v, err := strconv.ParseUint(*initialTargetS, 0, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --initial-target %q: %v\n", *initialTargetS, err)
			os.Exit(1)
		}
		initialTarget = v
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("network: %s  chainID: %d", net.name, net.chainID)
	log.Printf("RandomX engine: %s", randomXImplementation())

	if !*noMine && !randomXIsCgo() && !*allowSlowMining {
		fmt.Fprintln(os.Stderr,
			"error: RandomX CGo engine is not compiled in.\n"+
				"       Mining with the pure-Go fallback (~1 H/s) is disabled for live networks.\n"+
				"       Pass --allow-slow-mining to override (testing only) or rebuild with CGo enabled.")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	numThreads := *threads
	if numThreads <= 0 {
		numThreads = runtime.NumCPU() - 1
		if numThreads < 1 {
			numThreads = 1
		}
	}

	if err := run(ctx, net, dir, *authrpc, *rpc, *jwt, *minerHex, *listen, *peersFlag, *noMine, *light, numThreads, initialTarget); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("fatal: %v", err)
	}
	log.Printf("%s node stopped cleanly", net.name)
}

func run(ctx context.Context, net networkCfg, dataDir, authrpc, rpcURL, jwtFile, minerHex, listenAddr, peersStr string, noMine, lightMode bool, numThreads int, initialTarget uint64) error {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// ── 1. wait for geth and verify genesis ──────────────────────────────────
	verifyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	log.Printf("waiting for geth at %s …", rpcURL)
	if err := waitForGeth(verifyCtx, httpClient, rpcURL); err != nil {
		return fmt.Errorf("geth not reachable: %w", err)
	}
	log.Printf("verifying genesis hash …")
	if err := consensus.VerifyGenesisMatch(verifyCtx, rpcURL, net.genesisHashHex); err != nil {
		return fmt.Errorf("genesis verification: %w", err)
	}
	log.Printf("genesis OK")

	// ── 2. open consensus store ───────────────────────────────────────────────
	store, err := consensus.OpenStore(dataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	// ── 3. seed genesis block if fresh DB ────────────────────────────────────
	if err := seedGenesis(store, net.genesisBlock, initialTarget); err != nil {
		return fmt.Errorf("seed genesis: %w", err)
	}

	// ── 4. Engine API client ──────────────────────────────────────────────────
	client, err := engine.NewClientFromFile(authrpc, jwtFile)
	if err != nil {
		return fmt.Errorf("engine client: %w", err)
	}

	// ── 5. startup recovery (spec §6.3) ──────────────────────────────────────
	if err := startupRecovery(ctx, store, client, httpClient, rpcURL); err != nil {
		return fmt.Errorf("startup recovery: %w", err)
	}

	// ── 6. parse miner address ────────────────────────────────────────────────
	minerAddr, err := consensus.AddrFromHex(minerHex)
	if err != nil {
		return fmt.Errorf("invalid --miner address: %w", err)
	}
	log.Printf("miner address: %s", minerHex)

	// ── 7. P2P layer ──────────────────────────────────────────────────────────
	peerClients := buildPeerClients(peersStr)

	// engineMu serialises all Engine API calls (newPayloadV3 + FCU) and the
	// associated canonical-chain store writes. Shared between makeAcceptFunc
	// (syncer reorgs) and the miner's submitBlock so they cannot interleave.
	var engineMu sync.Mutex

	acceptFn := makeAcceptFunc(client, store, &engineMu)

	relayFn := func(b *consensus.ConsensusBlock) {
		for _, peer := range peerClients {
			go func(c *p2p.Client) {
				pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := c.PushBlock(pushCtx, b); err != nil {
					log.Printf("[p2p] push block height=%d: %v", b.Header.Height, err)
				}
			}(peer)
		}
	}

	syncer := NewSyncer(store, acceptFn, relayFn)

	if listenAddr != "" {
		// Dedicated verification engine — 2-slot epoch cache so that blocks from
		// epoch N-1 (late arrivals, reorg candidates) can be verified without
		// re-initialising when the miner has already advanced to epoch N.
		verifyPow := newEpochVerifier(func() consensus.PoWEngine {
			return newRandomXEngine(lightMode)
		})
		defer verifyPow.Close()
		var verifyMu sync.Mutex
		powValidator := p2p.PoWValidator(func(hdr *consensus.ConsensusBlockHeader) bool {
			epochKey, err := consensus.EpochKey(hdr.Height, store.BlockHashAt())
			if err != nil {
				log.Printf("[p2p] pow verify: epoch key for height=%d: %v", hdr.Height, err)
				return false
			}
			verifyMu.Lock()
			defer verifyMu.Unlock()
			if err := verifyPow.Init(epochKey); err != nil {
				log.Printf("[p2p] pow verify: randomx init: %v", err)
				return false
			}
			serialized := hdr.Serialize()
			hashBytes := verifyPow.Hash(serialized[:])
			var h32 [32]byte
			copy(h32[:], hashBytes)
			return consensus.HashMeetsTarget(h32, hdr.Difficulty)
		})

		srv := p2p.NewServer(store,
			p2p.WithReceiver(func(b *consensus.ConsensusBlock) error {
				return syncer.Receive(ctx, b)
			}),
			p2p.WithPoWValidator(powValidator),
			p2p.WithChainID(net.chainID),
			p2p.WithGenesisHash(net.genesisHashHex),
		)
		go func() {
			log.Printf("[p2p] listening on %s", listenAddr)
			httpSrv := srv.NewHTTPServer(listenAddr)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[p2p] server error: %v", err)
			}
		}()
	}

	// IBD: sync from the best peer if we are behind. Retried in background
	// because peers may not be listening yet at startup time.
	if len(peerClients) > 0 {
		go func() {
			for {
				if err := syncFromPeers(ctx, store, syncer, peerClients,
					net.chainID, net.genesisHashHex); err != nil {
					log.Printf("[p2p] IBD sync error: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(15 * time.Second):
				}
			}
		}()
	}

	// ── 8. periodic stats (every 60 s) ───────────────────────────────────────
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				printStats(ctx, httpClient, rpcURL, store, minerHex)
			}
		}
	}()

	// ── 9. run mining loop (or relay-only if --no-mine) ──────────────────────
	if noMine {
		log.Printf("relay-only mode: no mining")
		<-ctx.Done()
		return ctx.Err()
	}

	pow := newMiningEngine(lightMode, numThreads)
	defer pow.Close()

	miner := NewMiner(store, client, pow, minerAddr, &engineMu)
	miner.onBlock = relayFn
	log.Printf("mining loop started")
	return miner.Run(ctx)
}

// seedGenesis writes the consensus genesis block to a fresh store.
// newGenesis must return the network-appropriate genesis block. For devnet,
// initialTarget may differ from the block's default (--initial-target override);
// for testnet/mainnet initialTarget always matches the block's baked-in value.
func seedGenesis(store *consensus.Store, newGenesis func() *consensus.ConsensusBlock, initialTarget uint64) error {
	_, _, err := store.GetTip()
	if err == nil {
		return nil // already initialized
	}
	if !errors.Is(err, consensus.ErrTipNotFound) {
		return err
	}
	genesis := newGenesis()
	if genesis.Header.Difficulty != initialTarget {
		// devnet only: --initial-target override. Recompute the consensus hash
		// since difficulty is part of the header bytes.
		genesis.Header.Difficulty = initialTarget
		genesis.Hash = consensus.GenesisHash(&genesis.Header)
	}
	if err := store.StoreBlock(genesis); err != nil {
		return fmt.Errorf("store genesis: %w", err)
	}
	log.Printf("consensus DB initialized at genesis: hash=%s  initial-target=%016x",
		consensus.HashToHex(genesis.Hash), initialTarget)
	return nil
}

// startupRecovery reconciles geth's execution head with the stored consensus
// tip (spec §6.3). Three cases:
//
//   - geth in sync at the right fork: confirm via forkchoiceUpdatedV3.
//   - geth in sync at the wrong fork: locate the common ancestor via
//     eth_getBlockByNumber scan, then replay our fork blocks.
//   - geth behind stored tip: replay missing execution payloads via
//     newPayloadV3 [fromH, tipH], then forkchoiceUpdatedV3 to the tip.
//   - geth ahead of stored tip: forkchoiceUpdatedV3 to the stored tip
//     (rolls geth back to our canonical chain).
//
// Geth must be started with --state.scheme path --syncmode full so that an
// unclean shutdown is recovered by journal replay rather than snap-sync.
func startupRecovery(ctx context.Context, store *consensus.Store, client engineAPI, httpCl *http.Client, rpcURL string) error {
	tipHash, tipHeight, err := store.GetTip()
	if err != nil {
		return fmt.Errorf("get tip: %w", err)
	}
	tipBlock, err := store.GetBlock(tipHash)
	if err != nil {
		return fmt.Errorf("get tip block: %w", err)
	}
	storedExecHex := consensus.HashToHex(tipBlock.Header.ExecutionPayloadHash)

	gethHeight, gethExecHex, err := gethLatestHead(ctx, httpCl, rpcURL)
	if err != nil {
		return fmt.Errorf("query geth head: %w", err)
	}

	log.Printf("startup: stored tip height=%d execHash=%.12s…", tipHeight, storedExecHex)
	log.Printf("startup: geth   head height=%d execHash=%.12s…", gethHeight, gethExecHex)

	switch {
	case gethHeight == tipHeight:
		if strings.EqualFold(gethExecHex, storedExecHex) {
			log.Printf("startup: in sync at height=%d", tipHeight)
			// fall through to callForkchoice below
		} else {
			log.Printf("startup: wrong fork at height=%d (geth=%.12s… stored=%.12s…) — locating common ancestor",
				tipHeight, gethExecHex, storedExecHex)
			commonH, err := findCommonAncestor(ctx, httpCl, rpcURL, store, tipHeight)
			if err != nil {
				return fmt.Errorf("startup: find common ancestor: %w", err)
			}
			log.Printf("startup: common ancestor at height=%d, replaying %d block(s)", commonH, tipHeight-commonH)
			return replayMissingBlocks(ctx, store, client, commonH+1, tipHeight)
		}
	case gethHeight > tipHeight:
		log.Printf("startup: geth is ahead (height=%d > stored=%d) — FCU to stored tip", gethHeight, tipHeight)
	default:
		log.Printf("startup: geth is behind (height=%d < stored=%d) — replaying %d block(s)",
			gethHeight, tipHeight, tipHeight-gethHeight)
		// After kill -9, geth v1.17+ stores the last FCU's finalized block in
		// LevelDB as a "forced head". On restart it refuses all newPayloadV3 calls
		// with "forced head needed for startup" until FCU is received. The stored
		// forced head points to a block geth no longer has in its execution store
		// (path-scheme dirty buffer was lost), so sending FCU(stored-tip) returns
		// SYNCING + "Fetching unknown head from peers" — still locked.
		//
		// Fix: send FCU pointing to geth's CURRENT canonical head (genesis after
		// kill-9 with path scheme). Geth always has genesis → returns VALID →
		// updates its stored finalized to genesis → clears the forced-head gate.
		// The replay that follows can then deliver blocks normally.
		if err := unlockBeaconForcedHead(ctx, client, gethExecHex); err != nil {
			return err
		}
		return replayMissingBlocks(ctx, store, client, gethHeight+1, tipHeight)
	}

	return callForkchoice(ctx, client, store, tipHeight, storedExecHex)
}

// unlockBeaconForcedHead clears geth's beacon-sync "forced head" gate by
// sending FCU pointing at geth's own current canonical head. After kill -9 with
// --state.scheme path, geth stores the last FCU's finalized block in LevelDB
// as a "forced head". On restart it rejects all newPayloadV3 with
// "forced head needed for startup" until FCU is received, but the stored head
// points to a block no longer in geth's execution store (dirty buffer was
// lost), so FCU(stored-tip) → "Fetching unknown head from peers" → still locked.
//
// FCU(geth's own head) always succeeds (geth has it → VALID) and updates the
// stored finalized, clearing the gate so the replay can proceed.
func unlockBeaconForcedHead(ctx context.Context, client engineAPI, gethCurrentExecHex string) error {
	fcs := engine.ForkchoiceStateV1{
		HeadBlockHash:      gethCurrentExecHex,
		SafeBlockHash:      gethCurrentExecHex,
		FinalizedBlockHash: gethCurrentExecHex,
	}
	result, err := client.ForkchoiceUpdatedV3(ctx, fcs, nil)
	if err != nil {
		return fmt.Errorf("startup unlock FCU: %w", err)
	}
	log.Printf("startup: unlock FCU to geth head status=%s", result.PayloadStatus.Status)
	switch result.PayloadStatus.Status {
	case "VALID", "SYNCING":
		return nil
	default:
		return fmt.Errorf("startup unlock FCU: unexpected status %s", result.PayloadStatus.Status)
	}
}

// callForkchoice calls forkchoiceUpdatedV3 pointing at the stored consensus
// tip. SYNCING is retried (geth may still be replaying its journal after a
// crash) until VALID is received or a 2-minute deadline expires.
func callForkchoice(ctx context.Context, client engineAPI, store *consensus.Store, tipHeight uint64, headExecHex string) error {
	safeExecHex := headExecHex
	finalExecHex := headExecHex

	if tipHeight >= 10 {
		if s, err := executionHashAtHeight(store, tipHeight-10); err == nil {
			safeExecHex = s
		}
	}
	if tipHeight >= 100 {
		if f, err := executionHashAtHeight(store, tipHeight-100); err == nil {
			finalExecHex = f
		}
	}

	fcs := engine.ForkchoiceStateV1{
		HeadBlockHash:      headExecHex,
		SafeBlockHash:      safeExecHex,
		FinalizedBlockHash: finalExecHex,
	}

	const maxWait = 2 * time.Minute
	deadline := time.Now().Add(maxWait)
	for {
		result, err := client.ForkchoiceUpdatedV3(ctx, fcs, nil)
		if err != nil {
			return fmt.Errorf("startup forkchoiceUpdatedV3: %w", err)
		}
		log.Printf("startup forkchoiceUpdatedV3 height=%d: %s", tipHeight, result.PayloadStatus.Status)
		switch result.PayloadStatus.Status {
		case "VALID":
			return nil
		case "SYNCING":
			if time.Now().After(deadline) {
				return fmt.Errorf("startup forkchoice at height %d still SYNCING after %s — "+
					"ensure geth is started with --state.scheme path --syncmode full", tipHeight, maxWait)
			}
			log.Printf("startup: geth SYNCING on FCU height=%d — retrying in 2s", tipHeight)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		default:
			return fmt.Errorf("unexpected startup forkchoice status: %s", result.PayloadStatus.Status)
		}
	}
}

// replayMissingBlocks delivers execution payloads for consensus heights
// [fromH, toH] to geth via newPayloadV3, then drives the canonical chain to
// toH via a retried forkchoiceUpdatedV3.
//
// After kill -9 with --state.scheme path, geth's in-memory state is at
// genesis, but its LevelDB canonical chain table has all block headers and
// bodies from before the crash (committed atomically per FCU). Geth's beacon
// sync marks the last-seen beacon head as a "forced head required" block;
// newPayloadV3 for any other block returns SYNCING and is discarded. The
// forced-head block itself is processed normally, triggering geth to
// re-execute the full chain from genesis using its LevelDB canonical data.
//
// Strategy: submit all payloads once (SYNCING is acceptable — some will be
// the forced-head block itself, others are already-known), then call FCU with
// a 2-minute retry loop. The FCU is what drives geth to complete the chain
// re-execution and advance its canonical head.
func replayMissingBlocks(ctx context.Context, store *consensus.Store, client engineAPI, fromH, toH uint64) error {
	if fromH > toH {
		return nil
	}
	log.Printf("startup: replaying execution payloads for heights %d–%d", fromH, toH)
	for h := fromH; h <= toH; h++ {
		b, err := store.GetBlockByHeight(h)
		if err != nil {
			return fmt.Errorf("startup replay: get block at height %d: %w", h, err)
		}
		payloadJSON, err := store.GetPayload(b.Hash)
		if err != nil {
			// Payload missing: the node likely crashed between StoreBlockOnly and
			// StorePayload. Re-fetch this block from a peer via IBD to recover.
			return fmt.Errorf("startup replay: payload for height %d (%.12s…) not in store — "+
				"node must IBD from a peer to recover: %w", h, consensus.HashToHex(b.Hash), err)
		}
		var payload engine.ExecutionPayloadV3
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return fmt.Errorf("startup replay: unmarshal payload at height %d: %w", h, err)
		}
		parentBeaconRoot := consensus.HashToHex(b.Header.ParentHash)

		status, err := client.NewPayloadV3(ctx, payload, []engine.Hash{}, parentBeaconRoot)
		if err != nil {
			return fmt.Errorf("startup replay: newPayloadV3 at height %d: %w", h, err)
		}
		switch status.Status {
		case "VALID", "ACCEPTED":
			log.Printf("startup: replayed height=%d status=%s", h, status.Status)
		case "SYNCING":
			// Expected during forced-head startup mode. Non-forced-head blocks are
			// discarded by geth, but the forced-head block itself is accepted. Geth
			// uses its LevelDB canonical data to re-execute the full chain from
			// genesis; the FCU below drives that to completion.
			log.Printf("startup: height=%d SYNCING (forced-head mode — FCU will drive recovery)", h)
		default:
			return fmt.Errorf("startup replay: newPayloadV3 at height %d status: %s (validationError: %v)",
				h, status.Status, status.ValidationError)
		}
	}
	execHex, err := executionHashAtHeight(store, toH)
	if err != nil {
		return fmt.Errorf("startup replay: get tip exec hash: %w", err)
	}
	return callForkchoice(ctx, client, store, toH, execHex)
}

// findCommonAncestor scans backward from upToHeight-1 to find the highest
// height where geth's canonical exec hash matches the stored consensus exec hash.
// Returns 0 (genesis) if no match is found before height 1.
func findCommonAncestor(ctx context.Context, httpCl *http.Client, rpcURL string, store *consensus.Store, upToHeight uint64) (uint64, error) {
	for h := upToHeight - 1; h >= 1; h-- {
		stored, err := executionHashAtHeight(store, h)
		if err != nil {
			continue
		}
		var block struct {
			Hash string `json:"hash"`
		}
		if err := plainRPC(ctx, httpCl, rpcURL, "eth_getBlockByNumber",
			[]any{consensus.QuantityHex(h), false}, &block); err != nil {
			continue
		}
		if strings.EqualFold(stored, block.Hash) {
			return h, nil
		}
	}
	return 0, nil
}

// ── plain JSON-RPC helpers (no JWT) ──────────────────────────────────────────

type rpcReqBody struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcRespEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func plainRPC(ctx context.Context, cl *http.Client, url, method string, params []any, out any) error {
	body, _ := json.Marshal(rpcReqBody{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env rpcRespEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("rpc error: %s", env.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func waitForGeth(ctx context.Context, cl *http.Client, url string) error {
	for {
		var v any
		if err := plainRPC(ctx, cl, url, "net_version", []any{}, &v); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func gethLatestHead(ctx context.Context, cl *http.Client, url string) (uint64, string, error) {
	var block struct {
		Hash   string `json:"hash"`
		Number string `json:"number"`
	}
	if err := plainRPC(ctx, cl, url, "eth_getBlockByNumber", []any{"latest", false}, &block); err != nil {
		return 0, "", err
	}
	height, err := consensus.ParseQuantity(block.Number)
	if err != nil {
		return 0, "", err
	}
	return height, block.Hash, nil
}

// avgBlockTime returns the mean inter-block interval over the last n blocks
// ending at headHeight, measured against geth's timestamps. Requires headHeight >= n.
func avgBlockTime(ctx context.Context, cl *http.Client, url string, headHeight, n uint64) (float64, error) {
	if headHeight < n {
		n = headHeight
	}
	if n == 0 {
		return 0, nil
	}
	type blockInfo struct {
		Timestamp string `json:"timestamp"`
	}
	fetchTS := func(h uint64) (int64, error) {
		var b blockInfo
		if err := plainRPC(ctx, cl, url, "eth_getBlockByNumber",
			[]any{consensus.QuantityHex(h), false}, &b); err != nil {
			return 0, err
		}
		v, err := consensus.ParseQuantity(b.Timestamp)
		return int64(v), err
	}
	newTS, err := fetchTS(headHeight)
	if err != nil {
		return 0, err
	}
	// Start from block 1 minimum — geth's genesis (block 0) has timestamp 0
	// (no timestamp in genesis.json) and would produce nonsensical averages.
	startHeight := headHeight - n
	if startHeight == 0 {
		startHeight = 1
		n = headHeight - 1
	}
	oldTS, err := fetchTS(startHeight)
	if err != nil {
		return 0, err
	}
	if newTS <= oldTS || n == 0 {
		return 0, nil
	}
	return float64(newTS-oldTS) / float64(n), nil
}

// printStats logs a one-line summary: height, avg block time, balance check.
func printStats(ctx context.Context, cl *http.Client, rpcURL string, store *consensus.Store, minerHex string) {
	sCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gethHeight, gethExecHash, err := gethLatestHead(sCtx, cl, rpcURL)
	if err != nil {
		log.Printf("[stats] geth query failed: %v", err)
		return
	}

	tipHash, tipHeight, err := store.GetTip()
	if err != nil {
		log.Printf("[stats] store tip query failed: %v", err)
		return
	}

	// avg block time over last 50 blocks (or however many we have)
	window := uint64(50)
	avg, err := avgBlockTime(sCtx, cl, rpcURL, gethHeight, window)
	if err != nil {
		log.Printf("[stats] avgBlockTime failed: %v", err)
	}

	// miner balance — use big.Int: balance exceeds uint64 after just 3 blocks
	// (4 × 5 CHK = 20e18 wei > uint64 max ~1.84e19).
	var balHex string
	if err := plainRPC(sCtx, cl, rpcURL, "eth_getBalance",
		[]any{minerHex, "latest"}, &balHex); err != nil {
		log.Printf("[stats] eth_getBalance failed: %v", err)
	}
	balWei := hexToBigInt(balHex)

	// expected = sum of block rewards 1..gethHeight (all equal before first halving)
	rewardWei := new(big.Int).SetUint64(consensus.BlockRewardWei(1))
	expectedWei := new(big.Int).Mul(rewardWei, new(big.Int).SetUint64(gethHeight))
	driftWei := new(big.Int).Sub(balWei, expectedWei)

	// sync: compare stored consensus tip's execution payload hash with geth's
	// latest block hash — both are the EVM block hash, so they must match.
	var storedExecHash string
	if tipBlock, err := store.GetBlock(tipHash); err == nil {
		storedExecHash = consensus.HashToHex(tipBlock.Header.ExecutionPayloadHash)
	}
	inSync := tipHeight == gethHeight && strings.EqualFold(storedExecHash, gethExecHash)
	syncStr := "IN-SYNC"
	if !inSync {
		syncStr = fmt.Sprintf("DIVERGED(consensus=%d/%s… geth=%d/%s…)",
			tipHeight, storedExecHash[:10], gethHeight, gethExecHash[:10])
	}

	log.Printf("[stats] height=%d  avg-block-time=%.2fs  balance=%s wei  expected=%s wei  drift=%s wei  %s",
		gethHeight, avg, balWei, expectedWei, driftWei, syncStr)
}

// buildPeerClients parses a comma-separated list of peer URLs and returns
// one p2p.Client per peer. Empty or blank entries are ignored.
func buildPeerClients(peersStr string) []*p2p.Client {
	if peersStr == "" {
		return nil
	}
	var clients []*p2p.Client
	for _, u := range strings.Split(peersStr, ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			clients = append(clients, p2p.NewClient(u))
		}
	}
	return clients
}

// makeAcceptFunc returns an AcceptFunc that calls newPayloadV3 and
// forkchoiceUpdatedV3 on the given engine client for each received block.
// mu must be the same mutex passed to NewMiner so that reorg FCU calls and
// miner FCU calls are mutually exclusive (CP2 EVM divergence fix).
func makeAcceptFunc(client engineAPI, store *consensus.Store, mu *sync.Mutex) AcceptFunc {
	return func(ctx context.Context, b *consensus.ConsensusBlock) error {
		var payload engine.ExecutionPayloadV3
		if err := json.Unmarshal(b.ExecutionPayload, &payload); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}

		mu.Lock()
		defer mu.Unlock()

		parentBeaconRoot := consensus.HashToHex(b.Header.ParentHash)
		status, err := client.NewPayloadV3(ctx, payload, []string{}, parentBeaconRoot)
		if err != nil {
			return fmt.Errorf("newPayloadV3: %w", err)
		}
		if status.Status != "VALID" && status.Status != "ACCEPTED" {
			return fmt.Errorf("newPayloadV3 status: %s (err: %v)", status.Status, status.ValidationError)
		}

		execHashHex := consensus.HashToHex(b.Header.ExecutionPayloadHash)
		_, tipHeight, _ := store.GetTip()
		safeExecHash, _ := executionHashAtHeight(store, safeBound(tipHeight))
		finalExecHash, _ := executionHashAtHeight(store, finalBound(tipHeight))
		if safeExecHash == "" {
			safeExecHash = execHashHex
		}
		if finalExecHash == "" {
			finalExecHash = execHashHex
		}

		fcs := engine.ForkchoiceStateV1{
			HeadBlockHash:      execHashHex,
			SafeBlockHash:      safeExecHash,
			FinalizedBlockHash: finalExecHash,
		}
		result, err := client.ForkchoiceUpdatedV3(ctx, fcs, nil)
		if err != nil {
			return fmt.Errorf("forkchoiceUpdatedV3: %w", err)
		}
		if result.PayloadStatus.Status != "VALID" {
			return fmt.Errorf("forkchoiceUpdatedV3 status: %s", result.PayloadStatus.Status)
		}
		// Update canonical tip inside the lock so that the miner's orphan check
		// (also inside chainMu) sees the new tip immediately. Without this, the
		// miner could acquire chainMu after acceptFn releases it but before
		// applyBlock's SetTip runs, pass the parent-hash check on the stale tip,
		// and call FCU(A-chain) after FCU(B-chain) — leaving geth on the wrong chain.
		// applyBlock's own SetTip call (outside chainMu) is a no-op after this.
		return store.SetTip(b.Hash, b.Header.Height)
	}
}

func safeBound(h uint64) uint64 {
	if h >= 10 {
		return h - 10
	}
	return 0
}

func finalBound(h uint64) uint64 {
	if h >= 100 {
		return h - 100
	}
	return 0
}

// syncFromPeers queries all peers for their tip height and syncs from the
// tallest one if it is ahead of the local tip or if we're on a divergent fork
// at the same height. Peers reporting a different chainID or genesisHash are
// skipped — they are on a different network.
func syncFromPeers(ctx context.Context, store *consensus.Store, syn *Syncer, peers []*p2p.Client, chainID uint64, genesisHash string) error {
	localHash, localHeight, err := store.GetTip()
	if err != nil {
		return err
	}

	var bestPeer *p2p.Client
	var bestHeight uint64
	var bestHash string
	for _, peer := range peers {
		st, err := peer.FetchStatus(ctx)
		if err != nil {
			log.Printf("[p2p] peer unreachable during IBD: %v", err)
			continue
		}
		// Reject peers on a different network.
		if st.ChainID != 0 && st.ChainID != chainID {
			log.Printf("[p2p] peer rejected: chain ID mismatch (got %d, want %d)", st.ChainID, chainID)
			continue
		}
		if st.GenesisHash != "" && !strings.EqualFold(st.GenesisHash, genesisHash) {
			log.Printf("[p2p] peer rejected: genesis hash mismatch (got %s, want %s)", st.GenesisHash, genesisHash)
			continue
		}
		if st.Height > bestHeight {
			bestHeight = st.Height
			bestHash = st.TipHash
			bestPeer = peer
		}
	}

	if bestPeer == nil {
		return nil
	}

	// Sync if peer is ahead OR if we're at the same height but on a different fork.
	localHashHex := consensus.HashToHex(localHash)
	if bestHeight < localHeight {
		return nil
	}
	if bestHeight == localHeight && strings.EqualFold(localHashHex, bestHash) {
		return nil // same tip — already in sync
	}

	// Use a Bitcoin-style block locator to find the common ancestor efficiently.
	// This handles deep forks without the O(depth) single-height probe approach.
	hashAt := func(h uint64) ([32]byte, error) {
		b, err := store.GetBlockByHeight(h)
		if err != nil {
			return [32]byte{}, err
		}
		return b.Hash, nil
	}
	locator := p2p.BuildLocator(localHeight, hashAt)
	fromHeight := uint64(1)
	if len(locator.Entries) > 0 {
		loc, err := bestPeer.FindCommonAncestor(ctx, locator)
		if err != nil {
			log.Printf("[p2p] locator failed, falling back to genesis sync: %v", err)
			fromHeight = 1
		} else {
			fromHeight = loc.CommonHeight + 1
		}
	}

	log.Printf("[p2p] IBD: local=%d/%s… peer_best=%d/%s… from=%d (locator) — syncing",
		localHeight, localHashHex[:10], bestHeight, bestHash[:10], fromHeight)
	fetch := func(ctx context.Context, from, limit uint64) ([]*consensus.ConsensusBlock, error) {
		return bestPeer.FetchBlocks(ctx, from, limit)
	}
	return syn.SyncFrom(ctx, fromHeight, bestHeight, fetch)
}

// hexToBigInt parses a 0x-prefixed hex string into a *big.Int.
// Returns zero on any error.
func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	n := new(big.Int)
	n.SetString(s, 16)
	return n
}
