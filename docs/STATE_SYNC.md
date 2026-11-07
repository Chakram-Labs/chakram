# State Sync — Pre-Mainnet Requirement

**Status:** Planned  
**Priority:** Must-have before mainnet  
**Effort:** ~1 week  

## Problem

The current sync strategy replays every block from genesis through the full pipeline:

```
CometBFT blocksync → ABCI FinalizeBlock → engine_newPayloadV3 → geth
```

This is correct and safe, but the per-block overhead is fixed regardless of block content — roughly 2 Engine API round-trips per block over localhost. On a cold start (Windows, 2026-11-07):

- 55,000 blocks → ~23 minutes
- 500,000 blocks → ~3.5 hours
- 1,000,000 blocks → ~7 hours
- 10,000,000 blocks → ~70 hours ← unacceptable

A chain producing 1 block/second reaches 1M blocks in ~11 days and 10M in ~4 months. Without state sync, validator onboarding becomes a multi-day process well before mainnet.

---

## Solution: CometBFT State Sync + Geth Snapshot

### How it works

CometBFT has a built-in state sync protocol. A new node:

1. Gets a trusted block height + app hash from the DNS seed or config
2. Finds peers that have a snapshot at or near that height
3. Downloads the snapshot in chunks (parallelised across peers)
4. Verifies the snapshot hash matches the trusted app hash
5. Starts from that height — no historical replay needed
6. Only syncs the last ~1000 blocks normally to reach the tip

For Chakram, the "app state" IS geth's EVM state. So a snapshot = a geth state export at a specific block.

### What needs to be built

#### 1. ABCI snapshot methods (in `abci/app.go`)

CometBFT calls these on snapshot-capable nodes:

```go
// Called by CometBFT to list available snapshots.
// Return snapshots at every Nth block (e.g. every 10,000 blocks).
func (app *ChakramApp) ListSnapshots(ctx context.Context,
    req *abcitypes.RequestListSnapshots) (*abcitypes.ResponseListSnapshots, error)

// Called when a syncing node offers us a snapshot to restore.
func (app *ChakramApp) OfferSnapshot(ctx context.Context,
    req *abcitypes.RequestOfferSnapshot) (*abcitypes.ResponseOfferSnapshot, error)

// Called to serve a chunk of our snapshot to a syncing peer.
func (app *ChakramApp) LoadSnapshotChunk(ctx context.Context,
    req *abcitypes.RequestLoadSnapshotChunk) (*abcitypes.ResponseLoadSnapshotChunk, error)

// Called to apply a received chunk during restoration.
func (app *ChakramApp) ApplySnapshotChunk(ctx context.Context,
    req *abcitypes.RequestApplySnapshotChunk) (*abcitypes.ResponseApplySnapshotChunk, error)
```

Currently these return empty/nil responses in the ABCI app — they are no-ops.

#### 2. Geth state export (snapshot producer)

A snapshot for Chakram = geth's chaindata at a specific finalized block. The snapshot producer runs on a full node (e.g. the VM):

- Every `snapshotInterval` blocks (e.g. 10,000), export geth's state
- Geth has `geth export` and the flat state exporter (`geth snapshot export`) for this
- The exported state is split into ~10 MB chunks for P2P transfer
- Each chunk is content-addressed (SHA256) so integrity is verifiable
- A manifest file lists all chunks + their hashes + the block height + app hash

The snapshot directory layout:
```
~/.chakram-node/snapshots/
  10000/
    manifest.json       # height, app_hash, chunk_count, chunk_hashes[]
    chunk-000.bin
    chunk-001.bin
    ...
  20000/
    ...
```

#### 3. Geth state import (snapshot consumer)

When `ApplySnapshotChunk` receives all chunks:
- Stop geth
- Wipe `geth-data/` 
- Reassemble chunks and run `geth import` or copy the chaindata directory
- Restart geth at the snapshot height
- Resume CometBFT blocksync from snapshot height → tip (usually a few hundred blocks)

#### 4. Trusted height distribution

The syncing node needs a trusted (height, app_hash) pair to verify the snapshot against. Two approaches (implement both):

**A. DNS TXT record** — extend `seeds.chakram.one` with a `snap=` field:
```
snap=55000:AABBCC...  (height:app_hash_hex)
```
Updated by seed operators whenever they produce a new snapshot.

**B. `--trusted-height` / `--trusted-hash` flags** — user can pass these manually, same as Cosmos chains. Useful when DNS is unavailable or the user wants to verify independently.

#### 5. chakram-node flag

```
chakram-node --state-sync          # enable state sync on this startup (default: off)
chakram-node --trusted-height N    # trusted block height for snapshot verification
chakram-node --trusted-hash 0xABC  # app hash at trusted height
```

If `--state-sync` is not passed, falls back to current full blocksync (safe default).

---

## Implementation Plan

| Step | What | File(s) |
|------|------|---------|
| 1 | Implement `ListSnapshots` + `LoadSnapshotChunk` (producer side) | `abci/app.go`, new `abci/snapshots.go` |
| 2 | Snapshot manager: produce + store snapshots every N blocks | `abci/snapshots.go` |
| 3 | Implement `OfferSnapshot` + `ApplySnapshotChunk` (consumer side) | `abci/snapshots.go` |
| 4 | Geth state import/export helpers | `cmd/chakram-node/snapshot.go` |
| 5 | Wire `--state-sync` flag into `cmdStart` | `cmd/chakram-node/commands.go` |
| 6 | Add `snap=` field to DNS seed resolution | `cmd/chakram-node/discovery.go` |
| 7 | Update VM to produce snapshots and publish `snap=` DNS record | ops |
| 8 | Integration test: fresh node syncs via state sync, verifies tip matches blocksync node | `scripts/devnet/` |

---

## Chunk Size and Snapshot Interval

- **Chunk size:** 10 MB — fits in one CometBFT P2P message, good parallelism across peers
- **Snapshot interval:** every 10,000 blocks (~2.8 hours at 1 block/sec) — keeps the "tail" sync short
- **Snapshot retention:** keep last 3 snapshots — allows nodes that are slightly behind to still find one
- **Max snapshot age for state sync:** 50,000 blocks — if the latest snapshot is older than this, fall back to full blocksync

---

## Security

- Snapshot integrity is verified against the app hash from the trusted height, which is committed into the CometBFT block header and signed by >⅔ validators
- A malicious snapshot provider cannot forge a snapshot that passes verification without breaking the BFT safety guarantee
- The trusted height + hash must come from a source the user trusts (DNS seed, hardcoded in binary, or manually provided)
- Snapshots are chunk-verified (each chunk has a SHA256 hash in the manifest) — partial corruption is detected before the state is applied

---

## Notes

- CometBFT's state sync protocol is well-tested on Cosmos chains (Osmosis, Cosmos Hub all use it)
- The main Chakram-specific complexity is the geth state export/import — geth does not natively support "restore to block N" cleanly; we may need to copy the full chaindata directory at snapshot time rather than using `geth export`
- Alternatively: at snapshot time, run `geth snapshot export` to get a flat key-value dump of the state trie, then `geth snapshot import` on restore — this is cleaner but needs testing on v1.17.6
- This feature should be developed on a branch and tested against a devnet before merging
