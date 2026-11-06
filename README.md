# Chakram v3

Chakram v3 replaces RandomX proof-of-work with CometBFT BFT consensus driving geth via the Engine API. The result is instant finality, deterministic block production, and full EVM compatibility.

## Architecture

```
CometBFT (consensus) ──Engine API (JWT)──► geth v1.17.6 (EVM execution)
      │                                          │
  ABCI app                                 ExecutionPayloadV3
  (this repo)                              (EIP-4895 withdrawals)
```

- **Consensus:** CometBFT v0.40.0, nop mempool, ABCI++ interface
- **Execution:** geth, chainID 11642 (devnet) / 11643 (testnet) / 1165 (mainnet)
- **Block reward:** 5 CHK/block base, halving every 8,409,600 blocks, delivered as EIP-4895 withdrawals
- **AppHash:** raw 32-byte execution block hash (instant finality means safe=finalized=head)

## Packages

| Package | Description |
|---|---|
| `engine/` | JSON-RPC client for Engine API (FCU, getPayload, newPayload) |
| `consensus/` | Block reward / halving schedule |
| `abci/` | CometBFT ABCI++ application (PrepareProposal → FinalizeBlock → Commit) |
| `cmd/chakram/` | `chakram init` / `chakram start` CLI |
| `cmd/genesis-tool/` | Generates 4-node devnet config (keys, genesis files, peer list) |

## Building

```bash
go build -o chakram-build ./cmd/chakram/
# with Byzantine test mode:
go build -tags faulty -o chakram-faulty ./cmd/chakram/
```

Requires Go 1.22+ and geth in `$PATH` (or `GETH=/path/to/geth`).

## Quick start (VM deployment)

```bash
git clone https://github.com/Chakram-Labs/chakram.git ~/chakram3
cd ~/chakram3

# Seed / solo node
bash scripts/deploy.sh --mode dev --reward-addr 0xYOUR_ADDRESS

# Validator joining the live network
bash scripts/deploy.sh --mode fullnode --reward-addr 0xYOUR_ADDRESS \
  --peers "NODE_ID@SEED_IP:26656"
```

The script installs Docker and Go, builds `chakramctl`, inits the node, and starts everything. Dashboard available at `http://VM_IP:8080`.

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full guide including GCP VM setup, firewall rules, validator registration, backups, and troubleshooting.

## Local dev (Docker)

Use `chakramctl` — the single entry point for all node operations.

```bash
# Build chakramctl once
go build -o chakramctl ./cmd/chakramctl/

# One-time init (generates keys, genesis, JWT secret, Docker volumes)
chakramctl init --mode dev

# Start
chakramctl up --mode dev
# Dashboard: http://127.0.0.1:8080

# Localnet (4 validators)
chakramctl init --mode localnet
chakramctl up --mode localnet
```

See [docs/NODE_OPERATIONS.md](docs/NODE_OPERATIONS.md) for the full operations guide.

## Native 4-node devnet (no Docker)

```bash
# Generate keys + genesis for all 4 nodes
go run ./cmd/genesis-tool/ --out scripts/devnet-4node/node-data

# Init + start all nodes
./scripts/devnet-4node/init-all.sh
./scripts/devnet-4node/start-all.sh

# Stop
./scripts/devnet-4node/stop-all.sh
```

## Key decisions

See [docs/DECISIONS.md](docs/DECISIONS.md) for all ADRs (D0–D20).

## Chain IDs

| Network | EVM chainID | CometBFT chain_id |
|---|---|---|
| devnet | 11642 | chakram-devnet-1 |
| testnet | 11643 | chakram-testnet-1 |
| mainnet | 1165 | chakram-1 |
