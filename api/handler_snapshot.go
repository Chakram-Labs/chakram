package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

type snapshotMetaFile struct {
	Height uint64 `json:"height"`
	Format uint32 `json:"format"`
	Chunks uint32 `json:"chunks"`
	Hash   string `json:"hash"`
}

type snapshotLatestResponse struct {
	Height    uint64 `json:"height"`
	Hash      string `json:"hash"`
	Chunks    uint32 `json:"chunks"`
	SizeBytes int64  `json:"size_bytes"`
}

// handleSnapshotLatest returns metadata for the most recent available snapshot.
// GET /api/snapshot/latest
func (s *Server) handleSnapshotLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	meta, totalSize, err := latestSnapshotMeta(s.cfg.HomeDir)
	if err != nil {
		writeError(w, http.StatusNotFound, "no snapshot available")
		return
	}
	writeJSON(w, http.StatusOK, snapshotLatestResponse{
		Height:    meta.Height,
		Hash:      meta.Hash,
		Chunks:    meta.Chunks,
		SizeBytes: totalSize,
	})
}

// handleSnapshotChainRLP streams the assembled geth chain.rlp for the latest snapshot.
// GET /api/snapshot/latest/chain.rlp
func (s *Server) handleSnapshotChainRLP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	meta, totalSize, err := latestSnapshotMeta(s.cfg.HomeDir)
	if err != nil {
		writeError(w, http.StatusNotFound, "no snapshot available")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="chakram-chain-%d.rlp"`, meta.Height))
	w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
	w.Header().Set("X-Snapshot-Height", strconv.FormatUint(meta.Height, 10))
	w.Header().Set("X-Snapshot-Hash", meta.Hash)
	w.WriteHeader(http.StatusOK)

	snapDir := snapshotDir(s.cfg.HomeDir, meta.Height)
	for i := uint32(0); i < meta.Chunks; i++ {
		chunkPath := filepath.Join(snapDir, fmt.Sprintf("chunk-%06d.bin", i))
		data, err := os.ReadFile(chunkPath)
		if err != nil {
			return // connection broken or chunk missing; can't send error mid-stream
		}
		if _, err := w.Write(data); err != nil {
			return
		}
	}
}

// latestSnapshotMeta returns the most recent snapshot manifest and total size.
func latestSnapshotMeta(homeDir string) (*snapshotMetaFile, int64, error) {
	base := filepath.Join(homeDir, "snapshots")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, 0, fmt.Errorf("no snapshots dir")
	}

	type candidate struct {
		meta snapshotMetaFile
		size int64
	}
	var candidates []candidate

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifestPath := filepath.Join(base, e.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var m snapshotMetaFile
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		// Sum chunk sizes and verify all chunks exist.
		var total int64
		allOK := true
		dir := snapshotDir(homeDir, m.Height)
		for i := uint32(0); i < m.Chunks; i++ {
			info, err := os.Stat(filepath.Join(dir, fmt.Sprintf("chunk-%06d.bin", i)))
			if err != nil {
				allOK = false
				break
			}
			total += info.Size()
		}
		if allOK {
			candidates = append(candidates, candidate{meta: m, size: total})
		}
	}

	if len(candidates) == 0 {
		return nil, 0, fmt.Errorf("no complete snapshots")
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].meta.Height > candidates[j].meta.Height
	})
	return &candidates[0].meta, candidates[0].size, nil
}

func snapshotDir(homeDir string, height uint64) string {
	return filepath.Join(homeDir, "snapshots", strconv.FormatUint(height, 10))
}
