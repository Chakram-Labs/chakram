# Chakram Architecture Decision Records

> Checked: 2026-11-05  
> CometBFT version: v0.40.0 (released 2026-07-27, Apache-2.0)  
> geth version: v1.17.6  
> Go version: 1.27

These ADRs record every significant design choice for Chakram. Each decision is immutable once a phase is deployed; later phases add new ADRs.

---

## D0. Fresh Start — No v2 Compatibility

### Context
Chakram 2.0 (RandomX PoW) was never deployed to mainnet and had no real users.

### Decision
Start a new git repository. No migration, no compatibility shims. Remove all PoW code (RandomX, LWMA-3, BadgerDB consensus store, desktop miner, legacy GUI, custom HTTP P2P).

### Rationale
No backwards compatibility debt means cleaner code, simpler reasoning, and freedom to redesign the consensus layer from scratch using CometBFT.

### Consequences
- v2 chain history is not carried forward.
- v2 repo (`chakram`) is archived for reference only.
- CometBFT chain IDs are distinct from v2 IDs (11641/11640/1164).

---

## D1. Payload Placement

### Context
CometBFT proposals are structured as a list of byte slices (`txs [][]byte`). The execution payload (geth `ExecutionPayloadV3`, an arbitrarily-sized JSON object) must be carried through the BFT consensus process so that all validators can call `engine_newPayloadV3` on the same execution block.

### Decision
The execution payload is carried as `txs[0]` — the first (and only) element of the CometBFT proposal's transaction list. A valid proposal contains **exactly one tx**: the execution envelope.

**Wire format:**
```
[0:4]  Magic bytes:  "CHKV"  (0x43 0x48 0x4B 0x56)
[4]    Version byte: 0x03    (v3)
[5:]   JSON body:   ExecutionEnvelope struct (see abci/envelope.go)
```

**ExecutionEnvelope JSON fields:**
| Field | Type | Description |
|---|---|---|
| `version` | uint8 | Matches wire version byte (3) |
| `execution_payload` | base64 string | JSON-encoded `ExecutionPayloadV3` (geth native format) |
| `versioned_hashes` | []base64 strings | For `newPayloadV3`; empty for PoA (no blobs) |
| `parent_beacon_block_root` | base64 string | 32 zero bytes for PoA |

**Encoding:** JSON with base64 for byte fields (not protobuf) for Phase 1 simplicity. Protobuf is the target encoding for Phase 2+ to reduce size. The magic+version prefix makes future encoding migrations detectable without breaking validators on old software.

### Rationale
- `txs[0]` is the simplest integration point: no CometBFT proto extensions needed.
- JSON+base64 avoids a proto codegen step in Phase 1, reducing build complexity.
- The magic prefix (`CHKV`) and version byte allow fast rejection of malformed or future-version envelopes.
- One tx per proposal makes validation logic simple: ≠1 tx → reject immediately.

### Consequences
- `max_bytes` must accommodate the largest plausible execution payload (see D9).
- CometBFT's mempool must not inject additional txs (see D2).
- Future protocol change: switch to protobuf outer envelope; version byte bumped to 0x04.

---

## D2. Mempool

### Context
CometBFT has a built-in mempool for gossipping transactions. geth also has a production mempool with gas pricing, nonce ordering, EIP-1559 fee market, and replacement logic. Running both in parallel adds complexity and creates ordering inconsistencies.

### Decision
CometBFT mempool is **disabled** using the `nop` mempool type (`mempool.type = "nop"` in `config.toml`). `CheckTx` always returns error code 1 with message "transactions must be submitted to geth RPC (port 8545)". `InsertTx` and `ReapTxs` (v0.40.0 app-mempool API) both return error code 1. `PrepareProposal` ignores any `req.Txs` and builds the payload exclusively via the Engine API.

### Rationale
- geth's mempool is a production system tuned for EVM semantics. Duplicating it in CometBFT adds maintenance cost with no benefit.
- The `nop` mempool is officially supported in CometBFT v0.38+ and v0.40.0.

### Consequences
- Users must submit transactions to `geth --http` (port 8545), not to any CometBFT RPC.
- No CometBFT-level tx indexing or mempool monitoring. Use geth's `txpool` API instead.
- `create_empty_blocks = true` must remain set (nop mempool constraint).

---

## D3. Timestamps

### Context
CometBFT uses BFT time (median of validator timestamps from the previous commit), which can lag behind wall time under adversarial conditions and could theoretically be ≤ the previous block's timestamp. geth enforces strict monotonicity: `payload.timestamp > parent.timestamp`.

### Decision
```
payloadTimestamp = max(cometBlockTime.Unix(), prevPayloadTimestamp + 1)
```
This rule is applied in both `PrepareProposal` (by the proposer) and `ProcessProposal` (deterministically by all validators). It is stored in `AppState.LastPayloadTime`.

**CometBFT consensus timeout configuration:**
```toml
timeout_propose     = "2s"
timeout_prevote     = "1s"
timeout_precommit   = "1s"
timeout_commit      = "500ms"
```
Target block time: **2 seconds**. Minimum effective block time: **1 second** (from the `+1` floor).

### Rationale
Under normal conditions, `cometBlockTime.Unix()` advances in real time, so the `+1` floor is rarely triggered. Under adversarial or testing conditions (fast replay, clock skew), the `+1` ensures geth never sees a non-monotonic timestamp. Both proposer and validators apply the same deterministic rule, so there is no disagreement.

### Consequences
- Under sustained adversarial conditions (>1/3 validators malicious), timestamps increment by 1 each block and diverge from wall time. This is expected BFT behavior.
- Timestamp rule is part of the application state machine: changing it is a consensus-breaking upgrade.

---

## D4. ABCI ↔ Engine API Mapping

### Context
CometBFT drives the application through a sequence of ABCI++ calls. Each call must be mapped to the appropriate Engine API call (if any) to keep geth's execution state in sync with CometBFT's consensus state.

### Decision

| ABCI Method | Trigger | Engine API Call(s) | Notes |
|---|---|---|---|
| `PrepareProposal` | Proposer builds block | `ForkchoiceUpdatedV3(fcs, attrs)` + `GetPayloadV3(id)` | Attrs include timestamp, withdrawals, prevRandao |
| `ProcessProposal` | All validators validate proposed block | `NewPayloadV3(payload, [], zeroHash)` | VALID→ACCEPT, INVALID→REJECT, SYNCING/ACCEPTED→REJECT (F1) |
| `FinalizeBlock` | All nodes after BFT commit | `NewPayloadV3` (if not yet validated) + `ForkchoiceUpdatedV3(nil)` | FCU advances head/safe/finalized |
| `Commit` | After FinalizeBlock | None (persist AppState to disk) | `app_state.json` updated |
| `Info` | On startup/handshake | None (read AppState from disk) | Returns `LastBlockHeight` + `LastBlockAppHash` |
| `InitChain` | Genesis (once) | None | Stores initial state |
| `CheckTx` | Tx submission to CometBFT | None | Always rejects (D2) |
| `ExtendVote` | After prevote | None | Returns empty extension |
| `VerifyVoteExtension` | Receiving vote extension | None | Always accepts |

**PrepareProposal detail:**
1. Compute `payloadTimestamp` per D3.
2. Compute `prevRandao` = hex-encoded `req.Hash` (previous CometBFT block hash, treated as 32 bytes). Deterministic and PoW-free.
3. Compute reward withdrawal: `{index: height-1, validatorIndex: 0, address: cfg.RewardAddress, amount: BlockRewardGwei(height)}`.
4. Build `ForkchoiceStateV1{head=lastExecHash, safe=lastExecHash, finalized=lastExecHash}`.
5. Call `ForkchoiceUpdatedV3(fcs, attrs)` → get `payloadID`.
6. Wait up to 1.8s (poll every 100ms) for payload readiness; call `GetPayloadV3(payloadID)`.
7. Encode `ExecutionPayloadV3` as `ExecutionEnvelope`, return `{Txs: [envelope]}`.

**ProcessProposal detail:**
1. Reject if `len(req.Txs) != 1`.
2. `DecodeEnvelope(req.Txs[0])` — reject on magic/version/parse error.
3. Check `payload.ParentHash == state.LastExecHash` — reject on mismatch.
4. Check `payload.Timestamp >= state.LastPayloadTime+1` — reject on violation.
5. Call `NewPayloadV3(payload, [], zeroHash)`.
6. `"VALID"` → ACCEPT; `"INVALID"` → REJECT; `"SYNCING"` or `"ACCEPTED"` → REJECT (F1: no optimistic accept).

**FinalizeBlock detail:**
1. If proposal was not validated in ProcessProposal (blocksync path), call `NewPayloadV3` now.
2. Call `ForkchoiceUpdatedV3({head: execHash, safe: execHash, finalized: execHash}, nil)` — F3: instant finality, all three fields set to the current block.
4. Compute `AppHash = execHash bytes` (raw 32 bytes, F4). AppHash for height H appears in the CometBFT header at height H+1.
5. Stage new state (do NOT persist yet — persist in Commit).
6. Return `ResponseFinalizeBlock{AppHash: appHash, TxResults: [{Code: OK}]}`.

**F1 change:** SYNCING/ACCEPTED → REJECT. The previous "optimistic accept" is removed. If a validator's geth is SYNCING, it rejects the proposal, causing the round to be re-proposed. This is safer than accepting an unvalidated payload that could split the chain if >1/3 of validators are in this state.

### Rationale
This mapping follows the BeaconKit (Berachain) pattern, which is the canonical prior art for CometBFT + Engine API. FinalizeBlock always calls FCU to advance the finalized hash, enabling geth to prune state aggressively.

### Consequences
- The proposer must have a responsive geth (≤1.8s for payload building). If geth is slow, the proposer will timeout and return an empty payload, causing the round to be re-proposed by the next validator.
- `finalizedBlockHash` advances by 1 every block (previous block). This is more aggressive than PoW (100-block lag) and may affect state pruning if `--gcmode` is not `archive` in devnet.

---

## D5. Source of Truth

### Context
In v2, both the consensus layer (BadgerDB) and geth (LevelDB) maintained canonical chain state. On restart, they were reconciled. In v3, CometBFT has its own block store.

### Decision
**CometBFT's block store is the single canonical store for consensus state.** geth's state is always derived from CometBFT's blocks via FinalizeBlock replay. geth does **not** need its own P2P sync; it only receives blocks via the Engine API from the local ABCI app.

On startup:
1. `Info` reads `AppState` from disk, returning `LastBlockHeight` and `LastBlockAppHash`.
2. CometBFT compares this to its block store height.
3. For each missing block (height `appHeight+1` to `cometHeight`), CometBFT calls `FinalizeBlock` + `Commit` to replay them into geth.

After a **geth data directory wipe**: restart chakram-v3. `Info` returns the committed height. CometBFT has all blocks in its block store, so it replays them all into geth via FinalizeBlock. geth fully rebuilds its state.

### Rationale
Eliminates the custom IBD/locator/syncer/reorg logic from v2. CometBFT's built-in block store and handshake protocol handle all of this.

### Consequences
- geth must be started with `--gcmode archive` in devnet to ensure all historical states are available for replay verification.
- CometBFT's block store size grows unboundedly. **RetainHeight=0** (F6) keeps all blocks, which is required for the replay path — if pruning is enabled, a wiped geth cannot replay from the pruned portion. Phase 3+ can introduce snapshots for fast sync instead.
- The replay path (FinalizeBlock for each historical block) calls `NewPayloadV3` + `ForkchoiceUpdatedV3` for every block. For a chain with thousands of blocks, this takes O(N × 2 RPC calls). Acceptable for Phase 1; fast sync via snapshots is a Phase 3 concern.

---

## D6. Health Gate

### Context
A validator whose geth node is behind (processing previous blocks, restarting) will call `ForkchoiceUpdatedV3` with attrs on a stale `headBlockHash`. geth will return a SYNCING payload, and the resulting proposal will be rejected by validators with a current geth. Repeated failed proposals waste rounds.

### Decision
A validator **does not call `ForkchoiceUpdatedV3` with `PayloadAttributes`** (i.e., does not produce a payload in PrepareProposal) while geth's latest block is more than 2 heights behind CometBFT's committed height.

When gated: PrepareProposal returns `{Txs: nil}` (empty proposal). CometBFT will timeout the propose step and move to the next round with the next proposer.

**Voting is not gated** — prevote and precommit proceed normally even if geth is behind. Only proposing is gated.

Health check: after each `Commit`, query geth's `eth_blockNumber` via the plain HTTP RPC (port 8545) asynchronously. Cache the result; do not block Commit.

### Rationale
The 2-block tolerance handles transient geth RPC latency without gating unnecessarily. Only the proposer role is suppressed; all other consensus activity continues.

### Consequences
- A lagging validator silently skips its propose turns. The round timeout is `timeout_propose = 2s`, so the chain adds ~2s of latency per lagging-validator turn.
- If all validators are behind (e.g., after a mass restart), all propose turns are skipped and the chain halts until geth catches up. This is the correct behavior.
- **Limitation (F2 note):** full vote gating (preventing prevote/precommit) is not achievable from ABCI without modifying CometBFT's consensus protocol. Only the propose step is gated. In practice this is sufficient — a lagging validator still validates proposals from other validators correctly once geth resumes.

---

## D7. AppHash

### Context
CometBFT's `AppHash` is the application-level Merkle root committed in the signed block header. Light clients verify `AppHash` to trustlessly verify application state. The choice of `AppHash` content determines what a light client can prove.

### Decision
```
AppHash = executionBlockHash_bytes  (raw 32 bytes, F4)
```
Where `executionBlockHash_bytes` is the 32-byte geth block hash (`ExecutionPayloadV3.BlockHash` decoded from hex) of the execution payload finalized at this height. Set directly as `ResponseFinalizeBlock.AppHash`. AppHash for height H appears in the CometBFT signed header at height H+1.

### Rationale
The execution block hash is the root of geth's Merkle Patricia Trie (state root, receipts root, transactions root). Using it directly (without sha256/keccak wrapping) avoids an unnecessary transformation and makes the AppHash derivation transparent:
1. Obtain a signed CometBFT header (verified by 2/3+ validator signatures).
2. Extract `AppHash` = the 32-byte execution block hash directly.
3. Use `AppHash` as the root to verify EVM proofs: account balances, storage slots, receipts.

**Previous implementation** (sha256 wrapper) was a deviation for implementation simplicity. F4 corrects this to the intended design.

### Consequences
- Android light client can verify any EVM state with only: (a) CometBFT light client proofs (validator set + signed headers) and (b) standard Ethereum `eth_getProof` Merkle proofs. No custom proof system required.
- AppHash changes every block (execution hashes differ). This is expected.
- Historical AppHash values are in CometBFT's block store; no separate index needed.

---

## D8. Validators and Rewards

### Context
v2 used RandomX PoW with open mining. v3 moves to a permissioned validator set initially (PoA), with PoS staking planned for later.

### Decision
**PoA for Phase 1:** Validator set is fixed in CometBFT genesis (`genesis.json`, `validators` array). No runtime validator set changes (`ValidatorUpdates` in `FinalizeBlock` response is always empty in Phase 1).

**Rewards:** EIP-4895 withdrawals, identical logic to v2. One withdrawal per block: `{index: height-1, validatorIndex: 0, address: cfg.RewardAddress, amount: BlockRewardGwei(height)}`. The reward schedule from `consensus/reward.go` is reused unchanged: 5 CHK base, halving every 8,409,600 blocks (~4 years at 2s/block), floor 0.3 CHK.

**Validator key → EVM address mapping:** Ed25519 validator keys do not directly map to secp256k1 EVM addresses. For Phase 1, the EVM reward address is configured separately via `--reward-address`. A staking contract (Phase 3) will formalize the mapping.

**Chain IDs:**
| Network | geth chainId | CometBFT chain_id |
|---|---|---|
| v3 devnet | 11642 | chakram-v3-devnet-1 |
| v3 testnet | 11643 | chakram-v3-testnet-1 |
| v3 mainnet | 1165 | chakram-v3-mainnet-1 |

v2 chain IDs (11641, 11640, 1164) are preserved for the running testnet; v3 uses distinct IDs.

### Rationale
PoA avoids all staking contract complexity for Phase 1. Reusing the withdrawal-based reward mechanism from v2 keeps the economic model consistent and lets the existing web explorer display rewards correctly.

### Consequences
- No slashing, no validator rotation in Phase 1.
- PoS transition (Phase 3) requires: (a) deploy staking contract on geth, (b) read staking events in FinalizeBlock, (c) return ValidatorUpdates to CometBFT.
- The reward address receives ~5 CHK every ~2 seconds on the single-validator devnet.

---

## D9. Consensus Params

### Context
CometBFT's consensus parameters control block size limits and timeout behaviour. They must be set correctly for large execution payloads and the target block time.

### Decision
```toml
[consensus]
timeout_propose     = "2s"
timeout_prevote     = "1s"
timeout_precommit   = "1s"
timeout_commit      = "500ms"

[mempool]
type = "nop"

[blocksync]
enable = true
```

Genesis `ConsensusParams`:
```json
{
  "block": {
    "max_bytes": 4194304,
    "max_gas": -1
  },
  "evidence": { "max_age_num_blocks": 100000, "max_age_duration": "172800s" },
  "validator": { "pub_key_types": ["ed25519"] },
  "version": { "app": 3 }
}
```

`max_bytes = 4194304` (4 MiB): accommodates a 30M gas block at typical Ethereum transaction density. geth's default gas limit is 30M. Each transaction is ~100–400 bytes. 4 MiB ≈ 10,000–40,000 bytes of raw transaction data plus the execution payload envelope overhead. This matches geth's own 4 MiB cap on the Engine API response size.

`max_gas = -1`: unlimited at the consensus layer. Gas limits are enforced by geth's EVM execution.

### Rationale
4 MiB is sufficient for Phase 1 devnet traffic. Phase 2/3 can increase this if needed via a consensus param update in `ResponseFinalizeBlock.ConsensusParamUpdates`. Timeouts are aggressive (2s propose + 1s + 1s + 0.5s = 4.5s maximum round time) matching the 2s target block time.

### Consequences
- With `timeout_commit = 500ms`, the practical floor for block time is ~500ms (commit wait) + ~200ms (payload building) = ~700ms in the best case. The `+1s` timestamp floor (D3) dominates at low load.
- Validators with high network latency may time out on prevote/precommit. Increase timeouts for high-latency Phase 2 networks.

---

## D10. Versions

### Context
Pinning exact versions ensures reproducible builds and avoids unexpected API changes.

### Decision
| Component | Version | Released | Source |
|---|---|---|---|
| CometBFT | v0.40.0 | 2026-07-27 | github.com/cometbft/cometbft |
| geth | v1.17.6 | (unchanged from v2) | github.com/ethereum/go-ethereum |
| Go | 1.26.3 | (from go.mod) | golang.org/dl |

CometBFT v0.40.0 was chosen because:
- It is the latest major stable release as of 2026-11-01 (checked via GitHub API: `v0.38.26` is a patch release on 0.38.x; `v0.40.0` is the latest major version release, dated 2026-07-27, well past the 2-week stability requirement).
- It includes the `nop` mempool, ABCI++ (PrepareProposal/ProcessProposal/FinalizeBlock/ExtendVote), and the app-managed mempool API (`InsertTx`/`ReapTxs`).
- It is the version used in production by Cosmos ecosystem chains as of late 2026.

geth v1.17.6 is unchanged: the Engine API (forkchoiceUpdatedV3, getPayloadV3, newPayloadV3) is identical between v2 and v3. No reason to upgrade.

Date checked: 2026-11-01.

### Consequences
- CometBFT v0.40.0 has the `InsertTx`/`ReapTxs` app-mempool interface. With `nop` mempool, these methods are called by the nop mempool reactor but always return an error — which is correct behavior per the nop mempool ADR.
- Future CometBFT upgrades may require ABCI interface changes. Pin and upgrade deliberately.

---

## D11. Licensing

### Context
Chakram v3 takes architectural inspiration from BeaconKit (Berachain's CometBFT + Engine API middleware).

### Decision
- **BeaconKit** (github.com/berachain/beacon-kit): BUSL-1.1 — used as **architecture reference only**. No code copied. All Chakram v3 code is original.
- **CometBFT** (github.com/cometbft/cometbft): Apache-2.0 — safe to use as a library.
- **go-ethereum (geth)**: LGPL-3.0 — used as an external process (not linked). No LGPL obligations for the consensus layer binary.
- **Chakram v3**: MIT license (same as v2).

### Rationale
BUSL-1.1 prohibits production use and derivative works without a commercial license. Using BeaconKit only as an architectural reference (reading docs, studying call sequences) does not create a derivative work. All implementation is from first principles using CometBFT's public Go API.

### Consequences
- No license compliance risk as long as BeaconKit code is never copied.
- MIT license on Chakram v3 allows anyone to run validators, including future PoS participants.

---

## D12. Validator → EVM Reward Address Mapping (PoA)

### Context
CometBFT validators use Ed25519 keys, which do not directly correspond to secp256k1 EVM addresses. Rewards must be delivered to an EVM address via EIP-4895 withdrawals.

### Decision
Static mapping: each node is configured with its own EVM reward address via `--reward-address`. The proposer of each block (determined by CometBFT's round-robin proposer selection) sets `feeRecipient` and the withdrawal recipient to its own configured address. A `validator-map.json` file in the devnet output directory documents the mapping for operators.

### Rationale
No on-chain staking contract is needed for Phase 1/2. The mapping is human-configurable and auditable. The genesis-tool generates the mapping deterministically.

### Consequences
- Changing validators requires regenerating genesis and restarting the network.
- In Phase 3 (PoS), a staking contract will formalize the mapping: `validatorPubKey → evmAddress` registered on-chain.
- For Phase 2 4-node devnet, node0→`0x1111...`, node1→`0x2222...`, node2→`0x3333...`, node3→`0x4444...`.

---

## D13. geth devp2p Peering for Tx Gossip (4-node devnet)

### Context
In the 4-node devnet, users may submit transactions to any node's geth RPC, but only the proposer's geth builds the execution payload. Transactions submitted to non-proposer nodes must reach the proposer's txpool before the payload is built.

### Decision
Each geth node runs in post-merge authenticated mode (Engine API active). In this mode, geth does **not** import blocks from devp2p peers — only the local ABCI app delivers blocks via newPayload. geth's devp2p layer is used only for txpool gossip. After all 4 geth nodes start, `wire-peers.sh` calls `admin_addPeer` on each node to connect them, enabling txpool gossip across all 4 geth nodes.

### Rationale
This is the correct Ethereum post-merge architecture: the consensus layer (ABCI app) drives block import; the execution layer (geth) handles only EVM execution and tx gossip. No custom tx propagation is needed.

### Consequences
- geth devp2p peering must be re-established after a geth node restarts. A future improvement is `static-nodes.json` generated from geth node keys.
- Tx gossiped to a non-proposer node has an additional ~1 gossip RTT of latency before it reaches the proposer's txpool. At 2s block time, this is negligible.

---

## D15. Atomic app_state.json Writes

### Context
`app_state.json` is written by `Commit` after every block. If chakram crashes between the file write and the OS rename, the file could be partially written. On restart, `loadState` would fail to parse the JSON, return an error that propagated as a startup failure, and the node would refuse to start.

### Decision
`saveState` writes to a temporary file (`app_state.json.tmp`) and then calls `os.Rename`, which is atomic on POSIX filesystems. A crash during the write leaves the `.tmp` file; the original `app_state.json` is unchanged.

If a corrupt `app_state.json` is found at startup (JSON parse error), `loadState` renames it to `app_state.json.corrupt` and returns an empty `AppState{}`. This triggers a full FinalizeBlock replay from block 1, which is safe (CometBFT block store is the source of truth, D5).

### Rationale
The previous `os.WriteFile` pattern was non-atomic: a crash mid-write produced a truncated JSON file that `json.Unmarshal` would reject, causing a startup failure with no recovery path. The temp-file+rename pattern is the standard POSIX idiom for atomic file updates.

### Consequences
- A crash between FinalizeBlock and Commit (the critical window) leaves `app_state.json` at height N-1. On restart, geth is at height N (FCU was called in FinalizeBlock). The geth-lag detection in Info() does not fire (geth is NOT behind), so CometBFT calls FinalizeBlock(N) again. NewPayloadV3 returns VALID (block already exists in geth). FCU advances finalized to N. Commit saves height N. Recovery is clean.
- CrashRecovery path tested by test l, m, and q.

---

## D16. Geth-Lag Detection in Info() (Restart/Replay)

### Context
When geth's data directory is wiped (deliberately or by hardware failure), geth restarts from genesis at height 0 while `app_state.json` records a much higher height. Without intervention, `Info()` reports the `app_state.json` height to CometBFT, which considers the node fully synced and skips the FinalizeBlock replay — geth never rebuilds its state.

### Decision
`Info()` queries `eth_blockNumber` on geth's plain HTTP RPC. If `geth_height < app_state.LastHeight - 1`, the reported height is rolled back to `geth_height` (or 0 if geth is at genesis) and `app_state.LastHeight` is updated in memory (not saved — it persists only after the replay's first Commit). CometBFT's handshake then detects the gap and replays FinalizeBlock for every missing block.

The tolerance is `>1` (not `>0`) because the one-block gap between FinalizeBlock(N) and Commit(N) is normal: geth is at N, app_state records N-1.

### Rationale
Makes geth-wipe recovery fully automatic and tested. Eliminates the need for manual `app_state.json` edits to force replay.

### Consequences
- On each restart, one extra RPC call is made to geth during the handshake. This is negligible.
- If geth is temporarily unavailable at startup (e.g., slow boot), `queryGethBlockNumber` times out and `Info()` falls back to `app_state.json` with a warning. The node starts normally; if geth is actually behind, the next PrepareProposal health gate (D6) will suppress proposals until geth catches up.
- Tested by test n.

---

## D17. Optimistic Payload Pre-Build (Fix 3)

### Context
The original PrepareProposal contained a hard-coded `time.After(1 * time.Second)` before calling `GetPayloadV3`. The 1s wait existed to give geth time to fill the payload with pending transactions after the `ForkchoiceUpdatedV3` call. This added a floor of ~1s to block time regardless of actual geth readiness.

### Decision
After each `Commit(N)`, a background goroutine calls `ForkchoiceUpdatedV3` with payload attributes for height N+1 (timestamp = `max(now, prevPayloadTime+1)`). The resulting `payloadID` is stored in `app.prebuilt` under the mutex.

When `PrepareProposal(N+1)` runs ~2s later (after CometBFT's prevote/precommit/commit rounds), it checks `app.prebuilt`. If the pre-built payload is for the correct height and has a timestamp ≥ the D3 minimum, it uses the stored `payloadID` and calls `GetPayloadV3` immediately with no wait. Geth has had ~2s to fill the payload.

If the pre-built payload is stale or absent (first block after restart, or CometBFT time jumped forward), `PrepareProposal` falls back to a fresh FCU + 1.5s poll window.

### Rationale
Eliminates the fixed 1s sleep. The 2s gap between blocks (timeout_propose + timeout_prevote + timeout_precommit + timeout_commit = 4.5s maximum; typically ~2s) gives geth sufficient time to collect pending txs and build the payload. GetPayload is then essentially instant (the payload is already complete).

### Consequences
- Block time is not artificially inflated by a fixed sleep.
- Non-proposer validators also call `preBuildPayload` after each Commit (they don't know who the next proposer is). The FCU call on non-proposers sets `head = finalized = safe = currentBlock`, which is a valid no-op from geth's perspective. The payloadID is unused on non-proposers.
- If the pre-built timestamp is `prevPayloadTime+1` and CometBFT's block time is significantly ahead (e.g., after a long pause), the D3 check `prebuilt.timestamp >= requiredTS` fails and the fallback path triggers. This is safe — it just adds one extra FCU+poll.

---

## D18. Blocksync Gap Requirement for Restart

### Context
CometBFT's blocksync module skips sync when `pool.height >= maxPeerHeight - 1` — i.e., when the restarting node is only 1-2 blocks behind. In that case, CometBFT enters consensus mode directly. Consensus mode relies on receiving the current round's commit certificates from peers, which are not re-gossiped for past rounds. A node that missed only 1-2 blocks and enters consensus mode cannot obtain those commit certs and gets stuck.

The previous integration_test.sh used `sleep 20` and `sleep 15` to ensure the chain had advanced ~10 blocks before the tested node restarted. Fixed sleeps are fragile: on a slow machine, 20s may not be enough; on a fast machine, they waste time.

### Decision
All restarts that require blocksync (tests l, n, and the node1 restore in test o) use a polling loop that waits until the reference node (node0) is **at least 10 blocks ahead** of the node being restarted before starting the restart:

```bash
wait_for_gap <idx> 10 <timeout_seconds>
```

This guarantees `maxPeerHeight - pool.height > 1`, forcing CometBFT into blocksync mode where it fetches and verifies all missing blocks from peers, then transitions to consensus.

### Rationale
10 blocks ≈ 20s at 2s/block. The polling loop is both faster on fast machines and correct on slow ones.

### Consequences
- If the chain is halted (e.g., quorum lost), `wait_for_gap` will time out and the test will proceed with a warning. The restart itself is still correct; it may just take longer to sync.
- Tests l, n, o, and q all use `wait_for_gap` instead of fixed sleeps.

---

## D19. Measured Timeouts

### Context
The original timeout configuration (from D3/D9) was estimated, not measured. The optimistic pre-build (D17) changes the timing profile: PrepareProposal no longer has a 1s fixed wait, so the actual bottleneck is the CometBFT round trip (propose → prevote → precommit → commit) plus geth's FCU+GetPayload latency.

### Decision
Based on observed behavior in the Phase 2 4-node devnet (localhost, no network latency):

| Operation | Observed latency |
|---|---|
| `ForkchoiceUpdatedV3` (with attrs) | <5ms |
| `GetPayloadV3` (pre-built, 2s window) | <2ms |
| `NewPayloadV3` (simple block, <100 txs) | 10–50ms |
| `NewPayloadV3` (replay, archive mode) | 20–100ms |
| Full PrepareProposal (with pre-build) | <10ms |
| CometBFT prevote round trip (localhost) | ~50ms |
| CometBFT precommit round trip (localhost) | ~50ms |

Timeout configuration retained from D3/D9:
```toml
timeout_propose     = "2s"   # >> actual PrepareProposal time; headroom for geth startup
timeout_prevote     = "1s"   # >> actual prevote RTT on LAN/localhost
timeout_precommit   = "1s"   # >> actual precommit RTT
timeout_commit      = "500ms" # geth FCU + proposal setup for next round
```

Observed block time: **~2s** at 0-load (dominated by `timeout_commit = 500ms` + prevote/precommit + D3 timestamp `+1` floor). At load, block time remains ~2s (the pipeline is gated by BFT rounds, not payload building).

### Rationale
The current timeouts provide >10× margin over observed latency on localhost. For a production WAN testnet (50–200ms RTT), `timeout_prevote` and `timeout_precommit` should be increased to `"500ms"` each and `timeout_propose` to `"3s"`. This is a Phase 3 tuning concern.

### Consequences
- At very high load (thousands of txs/block), `NewPayloadV3` may take >500ms. The `timeout_commit` must be ≥ max expected FinalizeBlock time. Current value is safe for devnet.
- `timeout_propose = 2s` sets the maximum wait for a proposer's PrepareProposal. With D6 (health gate), a lagging proposer returns an empty proposal immediately rather than waiting the full 2s.

---

## D20. geth HTTP Virtual-Host Restriction (DNS Rebinding Defence)

### Context
geth's HTTP JSON-RPC server checks the `Host` header on every request and rejects those that don't match the allowlist (`--http.vhosts`). The previous configuration used `"*"`, which accepts any `Host` value.

### Decision
Set `--http.vhosts` and `--authrpc.vhosts` explicitly for every geth service to the Docker service name plus `localhost` (e.g. `geth-dev,localhost` for the dev service). Remove the wildcard.

### Rationale
A wildcard `vhosts` enables DNS rebinding: a malicious web page registers a DNS name that resolves to the Docker container's IP. A browser same-origin request to that name carries a `Host` header matching `*`, so geth accepts it and the attacker can call arbitrary JSON-RPC methods (balance queries, transaction submission). Restricting to the exact service name — the only legitimate internal caller — removes that surface. `localhost` is added so host-side tooling (`curl`, scripts on the Docker host) still works via the mapped `127.0.0.1` port. The authrpc endpoint is also restricted even though it is JWT-protected; least privilege is the right default.

### Consequences
- Callers inside the Docker network must use the canonical service-name URL (which they already do).
- External tooling must connect via `127.0.0.1` or `localhost` on the mapped port — not a raw container IP.
- No functional change in normal operation.

---

## D14. Faulty Proposer Test Mode

### Context
Phase 2 requires a way to test Byzantine fault tolerance: one validator produces invalid proposals, and the remaining 3 must still reach consensus.

### Decision
A separate build tag `faulty` enables the `faultyApp` wrapper (in `cmd/chakram/faulty.go`). When `--faulty-rate R` is passed, each `PrepareProposal` call corrupts the envelope bytes with probability R by XOR-ing bytes 10–29 of the wire payload, producing an envelope that fails `DecodeEnvelope` or parent-hash validation in `ProcessProposal` on all other nodes. The stub `faulty_stub.go` (build tag `!faulty`) makes `maybeFaultyApp` a no-op so release builds are unaffected.

### Rationale
Byzantine fault tolerance is a core CometBFT property (tolerates f < n/3 faulty). The test confirms our ABCI implementation correctly rejects invalid proposals and advances the round via prevote-timeout rather than accepting a corrupt payload.

### Consequences
- Build with `go build -tags faulty` to produce a test-only binary.
- Never ship `-tags faulty` in production or testnet builds.
- The faulty rate is 1.0 (always corrupt) for Phase 2 test n, which is the strongest test of BFT tolerance with n=4, f=1.

---

## D21. Localnet persistent_peers Wired at Genesis Time

### Context
A 4-validator localnet where each node has no `persistent_peers` configured will never form consensus: CometBFT nodes dial peers on startup, and with an empty list each node is isolated. Test f was timing out because blocks were never produced.

### Decision
`genesis-tool` now computes the CometBFT node ID for each validator during key generation (before writing per-node files). The node ID is `hex(sha256(ed25519_pubkey)[0:20])`, matching CometBFT's derivation. Each node's `config.toml` is written with a `persistent_peers` line containing the node IDs and Docker service addresses of all other validators (`nodeID@chakram-node{j}:26656`). `setup.sh init_node()` copies `config.toml` into the comet volume alongside the other genesis files. `validator-map.json` also records `node_id` per entry for external tooling.

### Rationale
Wiring peers at genesis-tool time is the right place: all node keys exist simultaneously, so the full mesh can be expressed without a second pass. Using Docker service names instead of IP addresses works because all nodes share the `chakram-net` bridge network, where the compose service name is DNS-resolvable. Computing the node ID from the key (rather than running `cometbft show-node-id`) avoids running containers during setup and keeps the tool hermetic.

### Consequences
- localnet nodes find each other immediately on startup with no operator intervention.
- The `node_id` field in `validator-map.json` is authoritative for the generated keys.
- If a node key is regenerated (new `genesis-tool` run), the node IDs and all `config.toml` files change together — there is no stale-peer risk.
- Single-node dev mode generates a config.toml with an empty `persistent_peers` line (correct — no peers).

---

## D22. PrepareProposal Returns Empty Proposal on Engine API Error

### Context
When geth is briefly unavailable (e.g. during `docker stop`/`start`), calls to `engine_forkchoiceUpdatedV3` or `engine_getPayloadV3` fail. Previously, these failures caused `PrepareProposal` to return `(nil, error)`. In CometBFT, a non-nil error from `PrepareProposal` may cause the node to panic and the container to exit. Without a restart policy the node stays dead after geth comes back.

### Decision
All Engine API failure paths in `PrepareProposal` now return `(&ResponsePrepareProposal{Txs: [][]byte{}}, nil)` — an empty proposal with no error — matching the existing health-gate behavior. An empty proposal has zero txs, so `ProcessProposal` rejects it (it requires exactly one tx), consensus is not reached, and `FinalizeBlock` is never called with invalid state. CometBFT times out the round and retries the next round. When geth recovers, the next `PrepareProposal` call succeeds and blocks resume.

### Rationale
Empty proposal → round timeout → retry is the correct CometBFT liveness pattern for a temporarily unavailable execution layer. Panicking on a transient network error violates the liveness requirement and is unnecessary: no block is committed, no state is corrupted, and the operator has already been informed (the health-gate log warning). The alternative — adding `restart: on-failure` to docker-compose — would also work but doesn't fix the root cause (returning an error from PrepareProposal).

### Consequences
- Blocks resume automatically within one round timeout (≤5s) after geth is available again.
- Test e (docker stop geth → restart → blocks resume) passes without container restart.
- `FinalizeBlock` still returns a fatal error if geth is down during block finalization (rare: would require geth to go down between ProcessProposal ACCEPT and FinalizeBlock). A future fix could apply the same pattern to FinalizeBlock if needed.

---

## D23. Phase 3 — Permissioned Proof-of-Stake (Staking Registry)

### Context
Phase 1/2 used a fixed PoA validator set hardcoded in genesis. Changing validators required regenerating genesis and restarting the network. This does not scale and blocks institutional adoption. Phase 3 introduces on-chain staking so validators can join and leave without a network restart.

Chakram's vision is a general-purpose Indian public blockchain — payments, stablecoins (INRT), digital credentials, governance, everyday apps. For this to work with Indian institutions and regulators, the validator set must be trustworthy and accountable in the early stages, while having a clear, public, on-chain path to full decentralization.

### Decision

**Stage 1 (Phase 3 — this ADR):** Permissioned PoS with a single owner key.
- `StakingRegistry.sol` deployed on geth at a known address.
- Owner (initially the Chakram deployer) must whitelist an address before it can register as a validator.
- Whitelisted addresses call `register(ed25519PubKey, rewardAddress)` and deposit a minimum CHK stake.
- Owner can `jail(validator)` and `unjail(validator)` for misbehaviour.
- CometBFT double-sign evidence arriving in `FinalizeBlock.ByzantineValidators` automatically jails the offending validator (no stake burn in Phase 3 — jail only).
- `FinalizeBlock` reads `ValidatorRegistered`, `ValidatorJailed`, `ValidatorUnjailed` events from the staking contract and returns the corresponding `ValidatorUpdates` to CometBFT.
- Unbonding period: 21 days (matches Cosmos ecosystem convention). Stake is locked during unbonding.

**Stage 2 (future ADR):** Replace single owner key with a 5-of-9 multisig of trusted Indian institutions. No code change to the staking contract — only the owner address changes.

**Stage 3 (future ADR):** Deploy an on-chain governance contract. CHK holders vote on validator whitelisting, parameter changes, and protocol upgrades. Owner role transferred to governance contract.

**Stage 4 (future ADR):** Community vote to remove the whitelist entirely — pure stake-based permissionless entry, same as Ethereum mainnet.

### Staking contract interface (Phase 3)

```solidity
// owner-only
function whitelist(address validator) external;
function removeWhitelist(address validator) external;
function jail(address validator) external;
function unjail(address validator) external;
function transferOwnership(address newOwner) external; // Stage 2: set to multisig

// validator-callable
function register(bytes32 ed25519PubKey, address rewardAddress) external payable;
function startUnbonding() external;
function withdraw() external; // callable after 21-day unbonding

// events (read by FinalizeBlock)
event ValidatorRegistered(address indexed validator, bytes32 ed25519PubKey, address rewardAddress, uint256 stake);
event ValidatorJailed(address indexed validator);
event ValidatorUnjailed(address indexed validator);
event UnbondingStarted(address indexed validator, uint256 unlockTime);
event Withdrawn(address indexed validator, uint256 amount);
```

### ABCI changes

- `FinalizeBlock` calls a new `ReadStakingEvents(blockHash)` helper that queries geth for logs from the staking contract address in the finalized block.
- Parsed events are converted to `[]abcitypes.ValidatorUpdate` and returned in `ResponseFinalizeBlock.ValidatorUpdates`.
- Jailed validators get `Power: 0` (CometBFT removes them from the active set).
- New validators get `Power: votingPower(stake)` — linear for Phase 3 (1 CHK = 1 power unit, capped at a max per validator to prevent dominance).

### Minimum stake

- Phase 3 devnet/testnet: 1,000 CHK
- Phase 3 mainnet: 10,000 CHK (can be changed by governance later)

### Rationale
- Permissioned start gives regulatory credibility for INRT stablecoin and institutional credential use cases.
- Explicit staged decentralization roadmap (Stages 1→4) ensures Chakram is not permanently centralized — the path out is public and on-chain.
- Jail-only slashing (no burn) in Phase 3 reduces risk for early validators and builds trust. Burn-based slashing can be added in Stage 3 via governance vote.
- 21-day unbonding matches Cosmos chains — well understood by the validator community.
- `transferOwnership` is built into the contract from day one so Stage 2 (multisig) requires no contract upgrade.

### Consequences
- Chakram is centralized in Stage 1. This must be publicly documented and communicated.
- The deployer key must be kept extremely secure (hardware wallet). Loss of this key in Stage 1 means no new validators can be added.
- `FinalizeBlock` becomes stateful with respect to the staking contract — a geth wipe will require replaying staking events from genesis to reconstruct the validator set, which is already handled by the existing FinalizeBlock replay path (D5).
- Phase 3 does not change the reward mechanism — block rewards still go to `cfg.RewardAddress` per node config (D8/D12). A future ADR can route rewards through the staking contract instead.

---

## D24. Chakram Vision — Use Case Layers

### Context
Chakram is designed as a general-purpose Indian public blockchain. The technical roadmap must be aligned with the intended use cases so architecture decisions serve the vision rather than drifting toward generic infrastructure.

### Decision

The Chakram ecosystem is planned in the following layers, each building on the previous:

| Layer | What | When |
|---|---|---|
| L1 — Base chain | CHK currency, EVM execution, BFT consensus | Phase 1/2 (done) |
| L2 — Staking/PoS | On-chain validator registry, permissioned → permissionless | Phase 3 (this) |
| L3 — Stablecoin | INRT (Indian Rupee-pegged token), collateral model TBD | Phase 4 |
| L4 — Identity | On-chain credentials (ERC-725), university certificates, digital signatures | Phase 4/5 |
| L5 — Governance | On-chain polling, voting, CHK-weighted proposals | Phase 5 |
| L6 — Payments | Everyday payments UX, mobile wallet, INRT transfers | Phase 5/6 |
| L7 — Decentralization | Remove validator whitelist, full permissionless PoS | Phase 6 |

### INRT design considerations (Phase 4 planning note)
- Collateral-backed model (like DAI) is preferable over algorithmic for regulatory reasons.
- Requires RBI engagement or operation under existing e-money frameworks.
- CHK collateral ratio and liquidation mechanisms are a separate ADR.

### Identity/credential considerations (Phase 4 planning note)
- ERC-725 is the standard for on-chain identity — compatible with existing EVM tooling.
- Issuer (university, government body) signs credential on-chain.
- Credential hash stored on Chakram; full document stored off-chain (IPFS or institution server).
- Verification is a pure on-chain read — no issuer involvement needed post-issuance.

### Rationale
Documenting the vision as an ADR anchors future technical decisions to the actual use cases. Every new feature — staking design, gas limits, block size, stablecoin model — should be evaluated against whether it serves these layers.

### Consequences
- Phase 3 staking design (D23) must support institutional validators (universities, fintechs) not just crypto-native operators.
- Block size and gas limits may need revisiting for payment-scale throughput before Phase 6.
- INRT requires a separate legal/compliance workstream running in parallel with technical development.
