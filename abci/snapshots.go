package abci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	abcitypes "github.com/cometbft/cometbft/abci/types"
)

const (
	snapshotFormat    = uint32(1)
	snapshotChunkSize = 10 * 1024 * 1024 // 10 MB per chunk
)

// snapshotMeta is the manifest written alongside chunk files.
type snapshotMeta struct {
	Height uint64 `json:"height"`
	Format uint32 `json:"format"`
	Chunks uint32 `json:"chunks"`
	Hash   string `json:"hash"` // 0x-prefixed 32-byte EVM block hash (= AppHash)
}

// SnapshotManager handles snapshot creation (producer) and restoration (consumer).
// Snapshots are stored at {home}/snapshots/{height}/ as a manifest + chunk-*.bin files.
// The chunk data is a geth RLP chain export (blocks 0..height) split into 10 MB pieces.
type SnapshotManager struct {
	home        string // CometBFT home dir
	gethDataDir string // --datadir for geth commands
	gethBin     string // path to geth binary (empty = producer disabled)
	interval    int64  // blocks between snapshots; 0 = disabled
	retain      int    // number of snapshots to keep on disk

	mu         sync.Mutex
	inProgress bool // true while a snapshot creation goroutine is running

	// Consumer state: populated by OfferSnapshot, consumed by ApplyChunk.
	pending *pendingRestore
}

type pendingRestore struct {
	meta   snapshotMeta
	chunks map[uint32][]byte
	got    uint32 // how many chunks received so far
}

func newSnapshotManager(home, gethDataDir, gethBin string, interval int64, retain int) *SnapshotManager {
	if retain <= 0 {
		retain = 3
	}
	return &SnapshotManager{
		home:        home,
		gethDataDir: gethDataDir,
		gethBin:     gethBin,
		interval:    interval,
		retain:      retain,
	}
}

func (sm *SnapshotManager) snapshotDir(height uint64) string {
	return filepath.Join(sm.home, "snapshots", strconv.FormatUint(height, 10))
}

// loadAll reads all complete snapshots from disk, sorted newest-first.
func (sm *SnapshotManager) loadAll() []snapshotMeta {
	base := filepath.Join(sm.home, "snapshots")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []snapshotMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m snapshotMeta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		// Only include snapshots where all chunk files exist.
		complete := true
		for i := uint32(0); i < m.Chunks; i++ {
			if _, err := os.Stat(sm.chunkPath(m.Height, i)); err != nil {
				complete = false
				break
			}
		}
		if complete {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Height > out[j].Height })
	return out
}

func (sm *SnapshotManager) chunkPath(height uint64, index uint32) string {
	return filepath.Join(sm.snapshotDir(height), fmt.Sprintf("chunk-%06d.bin", index))
}

// ── Producer: snapshot creation ───────────────────────────────────────────────

// MaybeSnapshot is called from Commit. If the interval is configured and height
// is a snapshot point, it starts snapshot creation in a background goroutine.
func (sm *SnapshotManager) MaybeSnapshot(height int64, execHash []byte) {
	if sm.interval <= 0 || sm.gethBin == "" || sm.gethDataDir == "" {
		return
	}
	if height%sm.interval != 0 {
		return
	}
	sm.mu.Lock()
	if sm.inProgress {
		sm.mu.Unlock()
		return
	}
	sm.inProgress = true
	sm.mu.Unlock()

	go func() {
		defer func() {
			sm.mu.Lock()
			sm.inProgress = false
			sm.mu.Unlock()
		}()
		if err := sm.createSnapshot(uint64(height), execHash); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot: create height=%d: %v\n", height, err)
		}
	}()
}

// createSnapshot runs geth export and splits the output into chunk files.
// NOTE: geth export opens the chaindb read-only. This may fail if geth holds an
// exclusive lock; in that case the error is logged and the snapshot is skipped.
func (sm *SnapshotManager) createSnapshot(height uint64, execHash []byte) error {
	tmpDir := sm.snapshotDir(height) + ".tmp"
	finalDir := sm.snapshotDir(height)

	if fileExistsABCI(filepath.Join(finalDir, "manifest.json")) {
		return nil // already done
	}

	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) // defensive cleanup on any early return

	chainFile := filepath.Join(tmpDir, "chain.rlp")
	cmd := exec.Command(sm.gethBin,
		"--datadir", sm.gethDataDir,
		"export", chainFile,
		"0", strconv.FormatUint(height, 10),
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("geth export: %w", err)
	}

	chunkCount, err := splitIntoChunks(chainFile, tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	os.Remove(chainFile)

	if chunkCount == 0 {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("geth export produced empty file")
	}

	manifest := snapshotMeta{
		Height: height,
		Format: snapshotFormat,
		Chunks: chunkCount,
		Hash:   "0x" + encodeHex(execHash),
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), data, 0644); err != nil {
		os.RemoveAll(tmpDir)
		return err
	}

	os.RemoveAll(finalDir)
	if err := os.Rename(tmpDir, finalDir); err != nil {
		os.RemoveAll(tmpDir)
		return err
	}

	fmt.Printf("snapshot: created height=%d chunks=%d\n", height, chunkCount)
	sm.pruneOld()
	return nil
}

func (sm *SnapshotManager) pruneOld() {
	metas := sm.loadAll()
	for i, m := range metas {
		if i >= sm.retain {
			os.RemoveAll(sm.snapshotDir(m.Height))
		}
	}
}

// splitIntoChunks splits srcFile into 10 MB chunk-NNNNNN.bin files in destDir.
// Returns the number of chunks written.
func splitIntoChunks(srcFile, destDir string) (uint32, error) {
	f, err := os.Open(srcFile)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, snapshotChunkSize)
	var idx uint32
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			path := filepath.Join(destDir, fmt.Sprintf("chunk-%06d.bin", idx))
			if werr := os.WriteFile(path, buf[:n], 0644); werr != nil {
				return idx, werr
			}
			idx++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return idx, err
		}
	}
	return idx, nil
}

// LatestMeta returns the most recent snapshot manifest, or nil.
func (sm *SnapshotManager) LatestMeta() *snapshotMeta {
	all := sm.loadAll()
	if len(all) == 0 {
		return nil
	}
	return &all[0]
}

// ── ABCI producer methods ──────────────────────────────────────────────────────

func (sm *SnapshotManager) ListSnapshots(_ context.Context, _ *abcitypes.RequestListSnapshots) (*abcitypes.ResponseListSnapshots, error) {
	metas := sm.loadAll()
	snaps := make([]*abcitypes.Snapshot, 0, len(metas))
	for _, m := range metas {
		hb, err := hexToBytes(m.Hash)
		if err != nil {
			continue
		}
		snaps = append(snaps, &abcitypes.Snapshot{
			Height: m.Height,
			Format: m.Format,
			Chunks: m.Chunks,
			Hash:   hb,
		})
	}
	return &abcitypes.ResponseListSnapshots{Snapshots: snaps}, nil
}

func (sm *SnapshotManager) LoadSnapshotChunk(_ context.Context, req *abcitypes.RequestLoadSnapshotChunk) (*abcitypes.ResponseLoadSnapshotChunk, error) {
	if req.Format != snapshotFormat {
		return &abcitypes.ResponseLoadSnapshotChunk{}, nil
	}
	data, err := os.ReadFile(sm.chunkPath(req.Height, req.Chunk))
	if os.IsNotExist(err) {
		return &abcitypes.ResponseLoadSnapshotChunk{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &abcitypes.ResponseLoadSnapshotChunk{Chunk: data}, nil
}

// ── ABCI consumer methods ──────────────────────────────────────────────────────

func (sm *SnapshotManager) OfferSnapshot(_ context.Context, req *abcitypes.RequestOfferSnapshot) (*abcitypes.ResponseOfferSnapshot, error) {
	if req.Snapshot == nil {
		return &abcitypes.ResponseOfferSnapshot{Result: abcitypes.ResponseOfferSnapshot_REJECT}, nil
	}
	if req.Snapshot.Format != snapshotFormat {
		return &abcitypes.ResponseOfferSnapshot{Result: abcitypes.ResponseOfferSnapshot_REJECT_FORMAT}, nil
	}
	sm.mu.Lock()
	sm.pending = &pendingRestore{
		meta: snapshotMeta{
			Height: req.Snapshot.Height,
			Format: req.Snapshot.Format,
			Chunks: req.Snapshot.Chunks,
			Hash:   "0x" + encodeHex(req.Snapshot.Hash),
		},
		chunks: make(map[uint32][]byte, req.Snapshot.Chunks),
	}
	sm.mu.Unlock()
	return &abcitypes.ResponseOfferSnapshot{Result: abcitypes.ResponseOfferSnapshot_ACCEPT}, nil
}

func (sm *SnapshotManager) ApplySnapshotChunk(_ context.Context, req *abcitypes.RequestApplySnapshotChunk) (*abcitypes.ResponseApplySnapshotChunk, error) {
	sm.mu.Lock()
	if sm.pending == nil {
		sm.mu.Unlock()
		return &abcitypes.ResponseApplySnapshotChunk{
			Result: abcitypes.ResponseApplySnapshotChunk_ABORT,
		}, nil
	}
	sm.pending.chunks[req.Index] = req.Chunk
	sm.pending.got++
	done := sm.pending.got >= sm.pending.meta.Chunks
	meta := sm.pending.meta
	chunks := sm.pending.chunks
	sm.mu.Unlock()

	if !done {
		return &abcitypes.ResponseApplySnapshotChunk{
			Result: abcitypes.ResponseApplySnapshotChunk_ACCEPT,
		}, nil
	}

	// All chunks received — restore app state and optionally import geth data.
	if err := sm.applyRestore(meta, chunks); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot: restore failed: %v\n", err)
		sm.mu.Lock()
		sm.pending = nil
		sm.mu.Unlock()
		return &abcitypes.ResponseApplySnapshotChunk{
			Result: abcitypes.ResponseApplySnapshotChunk_RETRY_SNAPSHOT,
		}, nil
	}

	sm.mu.Lock()
	sm.pending = nil
	sm.mu.Unlock()
	return &abcitypes.ResponseApplySnapshotChunk{
		Result: abcitypes.ResponseApplySnapshotChunk_ACCEPT,
	}, nil
}

// applyRestore writes app_state.json and, if geth is configured, imports the
// assembled chain file. This call blocks until restoration is complete.
func (sm *SnapshotManager) applyRestore(meta snapshotMeta, chunks map[uint32][]byte) error {
	execHash, err := hexToBytes(meta.Hash)
	if err != nil {
		return fmt.Errorf("parse snapshot hash: %w", err)
	}

	// If geth is configured, assemble chunks and run geth import.
	// IMPORTANT: this only works if geth is NOT currently running (leveldb exclusive lock).
	// In the normal fast-sync flow the user runs 'chakram-node snapshot download' first
	// (before starting geth), so geth is not running here.
	// If geth IS running, the import is skipped and geth must already have the data.
	if sm.gethBin != "" && sm.gethDataDir != "" {
		syncDir := filepath.Join(sm.home, "state-sync")
		if err := os.MkdirAll(syncDir, 0755); err != nil {
			return err
		}
		chainFile := filepath.Join(syncDir, "chain.rlp")
		if err := assembleChunks(chunks, meta.Chunks, chainFile); err != nil {
			return fmt.Errorf("assemble chunks: %w", err)
		}

		fmt.Printf("snapshot: running geth import height=%d (this may take several minutes)...\n", meta.Height)
		if err := runGethImport(sm.gethBin, sm.gethDataDir, chainFile); err != nil {
			os.Remove(chainFile)
			return fmt.Errorf("geth import: %w", err)
		}
		os.Remove(chainFile)
	}

	// Write app_state.json — this is what tells Info to return snapshot height.
	state := &AppState{
		LastHeight:           int64(meta.Height),
		LastExecHash:         execHash,
		ValidatorPubKeys:     make(map[string][]byte),
		ValidatorRewardAddrs: make(map[string]string),
	}
	return saveState(sm.home, state)
}

func assembleChunks(chunks map[uint32][]byte, total uint32, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	for i := uint32(0); i < total; i++ {
		data, ok := chunks[i]
		if !ok {
			return fmt.Errorf("missing chunk %d", i)
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func runGethImport(gethBin, gethDataDir, chainFile string) error {
	cmd := exec.Command(gethBin,
		"--datadir", gethDataDir,
		"import", chainFile,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func encodeHex(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}

func fileExistsABCI(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
