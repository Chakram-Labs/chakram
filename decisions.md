# Chakram 2.0 Decisions (Phase 0)

Locked parameters for Chakram 2.0. Changing any of these after the public testnet requires an explicit decision recorded here.

## Network identity

| Parameter | Value | Notes |
|---|---|---|
| Mainnet chain ID | `1164` | Free in ethereum-lists/chains as of 2026-10-09. Register via PR once the testnet RPC is live. |
| Testnet chain ID | `11640` | Free in ethereum-lists/chains as of 2026-10-09. |
| Native currency | Chakram (`CHK`), 18 decimals | Confirm the ticker isn't confusingly close to an existing token before mainnet. |
| Launch | Fresh genesis, no premine | Genesis allocates nothing except system contracts (Phase 5). v1.x is not migrated. |

## Blocks and timing

| Parameter | Value | Notes |
|---|---|---|
| Target block time | 15 seconds | ~2,102,400 blocks per year |
| Difficulty algorithm | LWMA-3 (from v1.1), retuned for a 15 s target | Window size finalized in the Phase 1 spec |
| Median-time-past | Last 11 blocks | Block timestamp must be greater than MTP |
| Future timestamp limit | Local time + 120 s | |
| Gas limit | 30,000,000 | ~95 simple transfers per second at 15 s blocks |

## Rewards and supply

| Parameter | Value |
|---|---|
| Initial block reward | 5 CHK (`5000000000000000000` wei) |
| Halving interval | Every 8,409,600 blocks (~4 years) |
| Tail emission | 0.3 CHK per block, forever |
| Rule | `reward = max(5 CHK >> halvings, 0.3 CHK)` |
| Delivery | One Engine API withdrawal per block, paid to the miner's address |

Withdrawal amounts in the Engine API are denominated in **Gwei**, so 5 CHK is encoded as `5000000000` Gwei.

Supply schedule:

| Era | Years | Reward per block | Issued in era |
|---|---|---|---|
| 0 | 0–4 | 5 CHK | 42,048,000 CHK |
| 1 | 4–8 | 2.5 CHK | 21,024,000 CHK |
| 2 | 8–12 | 1.25 CHK | 10,512,000 CHK |
| 3 | 12–16 | 0.625 CHK | 5,256,000 CHK |
| 4 | 16–20 | 0.3125 CHK | 2,628,000 CHK |
| Tail | 20+ | 0.3 CHK | ~630,720 CHK per year |

About 81.5 million CHK are issued in the first 20 years, then a small, constant tail keeps miners paid. The tail exists so security never depends on fees alone.

## Fees

| Parameter | Value | Notes |
|---|---|---|
| Base fee (EIP-1559) | Burned, geth default | No geth fork needed. Partly offsets tail emission. |
| Priority fees | Paid to the block's fee recipient (the miner) | |

## Finality and reorgs

| Parameter | Value | Notes |
|---|---|---|
| Finalized hint to geth | Head minus 100 blocks (~25 min) | Sent as `finalizedBlockHash` in `forkchoiceUpdated` |
| Safe hint to geth | Head minus 10 blocks (~2.5 min) | Sent as `safeBlockHash` |
| Maximum reorg depth | 100 blocks | Nodes reject reorgs deeper than the finalized hint |
| Recommended exchange confirmations | 100 | Revisit once hashrate is established |

## Proof-of-work security

| Parameter | Value | Notes |
|---|---|---|
| Algorithm | RandomX with custom Chakram parameters | Makes pointing unmodified Monero setups at Chakram impossible without reconfiguring. A speed bump, not full protection. |
| Merge-mining with Monero | Planned upgrade after mainnet | Real defense against redirected hashpower. Tari is the reference implementation. |
| Checkpoints | Hardcoded checkpoints in each release | Safety net until merge-mining is live |
| Bitcoin anchoring | Designed in Phase 4, live after launch | |

## Execution layer

| Parameter | Value | Notes |
|---|---|---|
| Client | geth, unmodified | |
| Version | `v1.17.6` — commit `3d84c6b2e3e9be032f8f2dcd9e900f89391fcdab` (released 2026-09-23) | See verification record below. Build: `go build ./cmd/geth` from the verified archive. |
| Engine API version | V3 (Cancun) | V4 required if/when Prague is activated |
| Ethereum forks | All forks through Cancun active at genesis, merged at genesis (`terminalTotalDifficultyPassed: true`). Prague activation block: TBD (Phase 5+). | |
| `prevRandao` | Set by the consensus client | Miner-influenced. Contracts must not use it for randomness; documented for developers. |
| `parentBeaconBlockRoot` | Chakram consensus block hash | Lets contracts verify Chakram consensus data (EIP-4788) |
| Auth | JWT shared secret between consensus client and geth | |

## Reused from v1.1

- RandomX engine and epoch key derivation (`pow.go`)
- LWMA-3 and compact target encoding (`difficulty.go`)
- Median-time-past and timestamp rules (`blockchain.go`)
- P2P framing, peer limits, deadlines, penalties (`p2p.go`)
- Atomic consensus block store (`storage.go`, minus UTXO data)

## Dropped from v1.1

- UTXO model, Ed25519 transaction signatures, mempool, coinbase transactions. geth handles accounts, transactions, signatures and the mempool.

## geth v1.17.6 verification record

**Why v1.17.6:** most recent stable release at least 14 days old as of 2026-10-09 (v1.17.7 was 2026-09-30, v1.17.8 was 2026-10-08 — both too recent).

**Verification chain:**

1. **Source archive SHA256** — download `https://github.com/ethereum/go-ethereum/archive/refs/tags/v1.17.6.tar.gz` and verify:
   ```
   shasum -a 256 geth-v1.17.6.tar.gz
   b358cd5abb0f5639cd2409877d73f3d5002d834c3e781734a1010dda1c0914ad
   ```
   This matches the value in the Homebrew `homebrew-core` formula at commit `7363df1624` (the commit that first published v1.17.6), an independently maintained repository.

2. **Commit GPG signature** — commit `3d84c6b2e3e9be032f8f2dcd9e900f89391fcdab` carries a PGP signature from GitHub's web-flow signing key:
   - Key ID: `B5690EEEBB952194`
   - Full fingerprint: `9684 79A1 AFF9 27E3 7D1A  566B B569 0EEE BB95 2194`
   - Confirmed on `keys.openpgp.org`
   - GitHub's API reports `verification.verified = true, reason = "valid"` for this commit
   - The tag is a lightweight tag pointing directly to this signed commit

3. **Tag type** — lightweight (no separate tag signature). The commit signature is the authoritative integrity proof.

**To re-verify when building from source:**
```sh
curl -sL -o geth-v1.17.6.tar.gz https://github.com/ethereum/go-ethereum/archive/refs/tags/v1.17.6.tar.gz
echo "b358cd5abb0f5639cd2409877d73f3d5002d834c3e781734a1010dda1c0914ad  geth-v1.17.6.tar.gz" | shasum -a 256 -c
tar -xzf geth-v1.17.6.tar.gz
cd go-ethereum-1.17.6
go build -o geth ./cmd/geth
```
