# Chakram

An EVM-compatible Layer 1 blockchain combining **CometBFT v0.40 BFT consensus** with **go-ethereum v1.17 execution**. Instant deterministic finality, full EVM compatibility, on-chain staking registry.

- **Chain ID:** 11640
- **RPC:** https://rpc.chakram.one
- **Dashboard:** https://node.chakram.one
- **Testnet chain ID:** `chakram-testnet-1`

---

## Architecture

```
Clients / dApps (MetaMask · Hardhat · ethers.js)
        │  JSON-RPC
        ▼
  go-ethereum v1.17  ──Engine API (JWT)──  CometBFT v0.40
  (EVM execution)                          (BFT consensus)
        │                                        │
  EIP-4895 withdrawals                     ABCI++ app
  (block rewards)                          (this repo)
        │                                        │
        └──────── StakingRegistry.sol ───────────┘
                  (validator lifecycle)
```

**Consensus:** CometBFT v0.40, nop mempool, ABCI++ interface. >⅔ of active validators must sign a block before it is committed — final immediately, no reorgs ever.

**Execution:** geth v1.17.6, unmodified EVM. Same opcodes, precompiles, and gas rules as Ethereum mainnet.

**Staking:** `StakingRegistry.sol` governs the full validator lifecycle on-chain — registration, stake, unbonding, slashing, jailing, and governance. 1 CHK = 1 voting power unit, capped at 100,000 CHK per validator.

**Block reward:** 5 CHK/block, delivered as EIP-4895 withdrawals. Halves every 8,409,600 blocks (~266 days at 1s blocks), floor at 0.3 CHK tail emission.

---

## Package map

| Package | Description |
|---|---|
| `abci/` | CometBFT ABCI++ app — PrepareProposal → FinalizeBlock → Commit |
| `api/` | HTTP API server — REST endpoints, faucet, staking mutations, dashboard |
| `cmd/chakram-node/` | Main binary — manages geth subprocess, CometBFT, API server |
| `cmd/genesis-tool/` | Generates genesis files and validator keys for new chains |
| `consensus/` | Block reward and halving schedule |
| `contracts/` | `StakingRegistry.sol` + deploy script |
| `engine/` | Engine API JSON-RPC client (FCU, getPayload, newPayload) |
| `staking/` | Reads StakingRegistry events from geth, converts to CometBFT ValidatorUpdates |
| `web/` | React dashboard — blocks, staking, validator panel, logs |
| `landing/` | chakram.one static landing page |
| `scripts/` | Deployment and release scripts |

---

## Building from source

```bash
# 1. Build the web dashboard (embedded into the binary)
cd web && npm ci && npm run build && cd ..
cp -r web/dist cmd/chakram-node/webdist

# 2. Build the binary
VERSION=$(grep -oE '[0-9]+\.[0-9]+\.[0-9]+' internal/version/version.go | head -1)
go build -tags webembed \
  -ldflags "-X chakram/internal/version.Version=v${VERSION}" \
  -o chakram-node ./cmd/chakram-node/

# 3. Run
./chakram-node
# Dashboard: http://localhost:8080
```

Requires Go 1.22+. The binary downloads and manages geth automatically on first run — no separate geth install needed.

**Cross-compile for Linux (for VM deployment):**
```bash
GOOS=linux GOARCH=amd64 go build -tags webembed \
  -ldflags "-X chakram/internal/version.Version=v${VERSION}" \
  -o chakram-node-linux-amd64 ./cmd/chakram-node/
```

---

## Quick start (end user)

Download the latest binary from [GitHub Releases](https://github.com/Chakram-Labs/chakram/releases/latest) and run it:

```bash
# Linux / macOS
chmod +x chakram-node && ./chakram-node

# Windows — double-click chakram-node.exe, or in PowerShell:
.\chakram-node.exe
```

On first run it:
1. Downloads and launches geth
2. Fetches the network genesis files via DNS
3. Generates your validator EVM key (`reward-key.json`)
4. Downloads a chain snapshot so you sync to the tip in seconds
5. Opens the dashboard at `http://localhost:8080`

---

## Staking

Validator lifecycle is governed by `StakingRegistry.sol`. During the permissioned phase, your EVM address must be whitelisted by the owner before you can register.

**Voting power:** proportional to stake. 1 CHK staked = 1 unit of CometBFT voting power, capped at 100,000 CHK per validator. No single validator can exceed the cap regardless of how much they stake.

**Permissionless triggers** (encoded as immutable contract constants — cannot be changed by owner):
- 548 days after deploy, **or**
- 20 active validators reached, **or**
- 66% governance vote passes (7-day window, 30-day cooldown between proposals)

Once any trigger fires, the whitelist requirement is permanently removed.

**Unbonding:** validators call `startUnbonding()` → wait the unbonding period (block-number based) → call `withdraw()` to reclaim stake. Whitelist status is preserved after withdrawal, so validators can re-register without owner action.

**Slashing:** the ABCI app can call `slash(address, bps)` via a trusted slasher address. Slashed validators are automatically jailed; owner must unjail manually.

---

## Decentralisation roadmap

| Stage | Description | Status |
|---|---|---|
| 1 | Single owner key | Now |
| 2 | 5-of-9 multisig ownership | Phase 5 |
| 3 | On-chain governance contract owns registry | Phase 6 |
| 4 | Permissionless — whitelist permanently removed | Auto-triggered |

---

## VM deployment (seed / genesis validator)

```bash
# Build all platforms, deploy to VM, upload to GCS, update latest.json
# Requires: go, node/npm, gsutil, ssh alias for the VM
./scripts/release-upload.sh
```

The node runs as a `systemd` service on the VM. No Docker in production.

**DNS bootnode discovery:** new nodes find the network via TXT records at `seeds.chakram.one` — no hardcoded IPs, no binary updates needed when seed nodes change.

---

## Local development (Docker Compose)

```bash
# Single-validator devnet
docker compose --profile dev up -d

# 4-validator localnet
docker compose --profile localnet up -d

# With Prometheus + Grafana monitoring
docker compose --profile dev --profile monitoring up -d
```

Dashboard available at `http://localhost:8080`.

---

## CLI reference

```
chakram-node                  Start node (opens dashboard at :8080)
chakram-node start            Same as above, with explicit flags
  --no-browser                Don't open dashboard in browser
  --genesis-node              Run as genesis/seed validator
  --p2p-host <hostname>       Announce this hostname for P2P (public nodes)
  --staking-contract <addr>   StakingRegistry contract address
chakram-node status           Print current height and peers
chakram-node stop             Graceful shutdown
chakram-node version          Print version
```

---

## Key decisions

See [docs/DECISIONS.md](docs/DECISIONS.md) for architecture decision records (D0–D25+).
