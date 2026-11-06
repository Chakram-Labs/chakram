# Chakram Node Operations Guide

## Prerequisites

- Docker 24+ with Compose v2 (`docker compose`)
- `chakramctl` binary on PATH — build once with:
  ```bash
  go build -o chakramctl ./cmd/chakramctl/
  ```
- 4 GB RAM minimum (8 GB recommended for localnet)
- 20 GB disk per node (archive mode)

---

## Quick Start

### Dev mode (single validator, faucet enabled)

```bash
chakramctl init --mode dev
chakramctl up --mode dev
```

A fresh EVM reward key is generated automatically and saved to
`docker/secrets/evm-key-dev.json`. Dashboard at http://127.0.0.1:8080.

### Localnet mode (4 validators)

```bash
chakramctl init --mode localnet
chakramctl up --mode localnet
```

Per-node reward keys saved to `docker/secrets/evm-key-node{0..3}.json`.
Dashboards: :8080 :8081 :8082 :8083

### Fullnode mode (sync from existing network)

```bash
# Export genesis files from a running validator node first, then:
chakramctl init --mode fullnode \
  --genesis-dir /path/to/genesis-dir \
  --peers <node-id>@<host>:26656 \
  --bootnodes enode://...

chakramctl up --mode fullnode
```

### With monitoring (Prometheus + Grafana)

```bash
chakramctl up --mode dev --monitoring
# Grafana:    http://127.0.0.1:3000
# Prometheus: http://127.0.0.1:9090
```

---

## chakramctl Commands

| Command | Description |
|---|---|
| `chakramctl init --mode dev` | One-time setup: JWT secret, genesis, volume init, key gen |
| `chakramctl init --mode localnet` | Same for 4-validator localnet |
| `chakramctl init --mode fullnode --genesis-dir <dir>` | Fullnode volume init from existing genesis |
| `chakramctl up --mode dev` | Start node services |
| `chakramctl up --mode dev --monitoring` | Start with Prometheus + Grafana |
| `chakramctl down` | Stop all services |
| `chakramctl down -v` | Stop and remove all data volumes |
| `chakramctl status` | Show height, peers, sync state |
| `chakramctl logs` | Follow container logs |
| `chakramctl logs --service chakram-node-dev` | Follow one service |
| `chakramctl keys list` | Show validator and node keys |
| `chakramctl keys export --out ./keys/` | Export keys to host directory |
| `chakramctl reset` | Wipe chain data (preserves keys) |
| `chakramctl backup --out backup.tar.gz` | Backup chain data |
| `chakramctl restore --from backup.tar.gz` | Restore chain data |
| `chakramctl version` | Print version |

---

## Node API Reference

The Node API runs on port 8080 bound to `127.0.0.1` on the host.

| Endpoint | Description |
|---|---|
| `GET /api/status` | Node health, height, peer count, chain ID |
| `GET /api/validator` | This node's validator address, voting power, reward balance |
| `GET /api/blocks?limit=20&offset=0` | Recent blocks (newest first) |
| `GET /api/block/{height}` | Block detail: hash, time, proposer, txs, exec hash |
| `GET /api/tx/{hash}` | Transaction detail: from, to, value, status, gas |
| `GET /api/address/{addr}` | EVM address: balance (wei), nonce, code |
| `GET /api/peers` | Connected CometBFT peers |
| `GET /api/logs` | SSE stream of node log lines |
| `POST /api/faucet` | Send 1 CHK from faucet (dev mode only, 1 req/IP/min) |
| `GET /metrics` | Prometheus metrics |
| `GET /api/openapi.json` | OpenAPI 3.0 specification |

### Faucet (dev mode only)

```bash
curl -X POST http://127.0.0.1:8080/api/faucet \
  -H 'Content-Type: application/json' \
  -d '{"address":"0xYOUR_ADDRESS"}'
```

The faucet signs a raw EIP-155 transaction in Go using the Hardhat dev key
(`0xf39Fd6...`) and sends it via `eth_sendRawTransaction`. Rate limited to
one request per IP per minute.

---

## Security Notes

- The Engine API (port 8551) is **never exposed** outside the Docker network.
- Geth HTTP RPC (`--http.vhosts`) is restricted to the service name and
  `localhost` — wildcard hosts are disabled (DNS rebinding defence, D20).
- Geth HTTP RPC, CometBFT RPC, and the Node API are bound to `127.0.0.1` on the host.
- The dashboard (port 8080) is bound to `127.0.0.1` only — use an HTTPS
  reverse proxy (nginx, Caddy) for public access.
- The JWT secret in `docker/secrets/jwt.hex` must be kept confidential.
- EVM reward keys in `docker/secrets/evm-key-*.json` are chmod 600 and are not
  committed. Back them up alongside your chain data.
- Named Docker volumes carry explicit names (`chakram_*`) regardless of the
  working directory — safe to run `chakramctl` from any location.

---

## Architecture

```
Host (127.0.0.1)
  :8080  ←── Dashboard + Node API ─────────────┐
  :26657 ←── CometBFT RPC                       │
  :8545  ←── Geth HTTP RPC                      │
                                                 │ Docker network (chakram-net)
                                         chakram-node
                                              │   │
                                    CometBFT  │   │  Node API (:8080)
                                              │   │
                                     geth  ←──┘   │
                                    :8551 (Engine API, internal only)
```

### How nodes sync

1. **Fresh start:** `chakramctl init` runs `setup.sh` which generates keys,
   genesis files, and initialises geth + CometBFT volumes as uid 1000.
2. **Blocksync:** A new fullnode connects to peers, syncs CometBFT blocks, and
   replays each `FinalizeBlock` call against geth via the Engine API.
3. **Geth wipe recovery:** If geth data is missing, `Info()` detects the
   height mismatch and resets to 0, forcing a full replay from genesis (D16).
4. **Consensus:** Once caught up, validators propose blocks. Non-validator
   fullnodes process blocks but never vote.

---

## Backup and Restore

```bash
# Backup
chakramctl backup --out backup-$(date +%Y%m%d).tar.gz

# Restore
chakramctl down
chakramctl restore --from backup.tar.gz
chakramctl up --mode dev
```

Manual backup via docker:
```bash
docker run --rm \
  -v chakram_geth-dev-data:/backup/geth-data:ro \
  -v chakram_chakram-dev-data:/backup/comet-data:ro \
  alpine tar -czf - -C /backup . > backup.tar.gz
```

---

## Upgrading

1. `chakramctl backup` — always backup first
2. `chakramctl down`
3. Pull the new image or rebuild: `chakramctl init --mode dev --rebuild`
4. `chakramctl up --mode dev`

---

## Troubleshooting

### Node stuck catching up

Check if geth is running:
```bash
chakramctl logs --service chakram-geth-dev
```

If geth crashed or its data was wiped, the node auto-recovers by replaying all
FinalizeBlock calls from the CometBFT blockstore (D16, D5). Allow ~1s per block.

### CometBFT BFT halt

With 4 validators, the chain halts if 2+ validators are offline (f < n/3).
Restart the missing validators and consensus resumes automatically.

### Faucet "rate limited"

The faucet enforces 1 request per IP per minute. Wait 60 seconds.

### Logs

```bash
# All containers
chakramctl logs

# Single service
chakramctl logs --service chakram-node-dev

# Or directly via docker
docker logs chakram-node-dev -f
```
