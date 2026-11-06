# Chakram v3 — Docker Node Setup Report

> Date: 2026-11-02  
> Status: Complete — single-validator dev node running locally

---

## What Was Built

A fully containerised local node package for Chakram v3, runnable with a single command. The stack brings up a complete blockchain node with dashboard, monitoring, and operator CLI.

### Components

| Component | Description |
|---|---|
| `docker/Dockerfile` | 3-stage build: Node 22 (dashboard), Go 1.27 (binaries), Alpine (runtime) |
| `docker/docker-compose.yml` | 4 profiles: `dev`, `localnet`, `fullnode`, `monitoring` |
| `docker/setup.sh` | One-time setup: JWT secret, image build, genesis generation, volume init |
| `docker/scripts/entrypoint.sh` | Container startup: auto-init, config.toml, tee logging |
| `cmd/chakramctl/` | Operator CLI: `init`, `up`, `down`, `status`, `logs`, `keys`, `reset`, `backup`, `restore` |
| `monitoring/` | Prometheus + Grafana dashboards, provisioned automatically |

### How to Start

```bash
cd docker
bash setup.sh --mode dev
docker compose --profile dev up -d
```

Dashboard at http://127.0.0.1:8080

---

## Bugs Fixed During Setup

### 1. geth `--unlock` flag removed (geth v1.17.6)

**Problem:** `docker-compose.yml` used `--unlock`, `--password /dev/null`, `--allow-insecure-unlock` — flags removed in geth v1.14+ when the personal API was dropped.  
**Fix:** Removed those three flags and dropped `personal` from `--http.api`.

### 2. Volume name mismatch

**Problem:** `setup.sh` created volumes with prefix `chakram_*` (e.g. `chakram_geth-dev-data`) but Docker Compose names volumes using the project name derived from the directory, producing `docker_*` (e.g. `docker_geth-dev-data`). Setup was writing to completely different volumes than what the containers mounted — containers started with empty, uninitialised volumes.  
**Fix:** Changed `init_node()` in `setup.sh` to use `docker_geth-${suffix}-data` and `docker_chakram-${suffix}-data`.

### 3. Root-owned volume directories (permission denied)

**Problem:** `geth init` runs as root inside the container, so `/data/geth/` was owned by root. The geth container runs as `user: "1000:1000"` and couldn't write to it.  
**Fix:** Added `docker run --rm -v "$geth_vol:/data" alpine chown -R 1000:1000 /data` after geth init in `setup.sh`.

### 4. Double `.chakram` nesting in comet volume

**Problem:** `setup.sh` wrote CometBFT config into `/comet/.chakram/config/` inside the volume. But `docker-compose.yml` mounts the volume at `/home/chakram/.chakram` (not `/home/chakram`), making the actual path `/home/chakram/.chakram/.chakram/config` — one level too deep. The entrypoint looked for `/home/chakram/.chakram/config` and couldn't find it, then tried to create it but failed because the volume root was root-owned.  
**Fix:** Changed `setup.sh` to write directly to `/comet/config/` and `/comet/data/` (volume root), and chown the entire volume root to `1000:1000`.

### 5. geth HTTP virtual host rejection (403 Forbidden)

**Problem:** Geth's HTTP server defaults to `vhosts=localhost`. When the chakram container calls `http://geth-dev:8545`, the `Host` header is `geth-dev` — geth returns 403. The health gate then can't query `eth_blockNumber`, skips every proposal, and the chain never advances past height 0.  
**Fix:** Added `--http.vhosts "*"` to the geth-dev command in `docker-compose.yml`.

### 6. Validator public key displayed as raw Go map

**Problem:** The Node API used `fmt.Sprintf("%v", pubKey)` on a `map[string]any`, producing `map[type:tendermint/PubKeyEd25519 value:JP84d9...]` in the dashboard.  
**Fix:** Added `pubKeyValue()` helper in `api/handlers.go` that extracts the `value` field from the map, showing only the clean base64 key.

---

## Current Node State (as of 2026-11-02)

- Single validator dev node producing blocks at ~2s/block
- CometBFT and geth both running and in sync
- Dashboard fully functional at http://127.0.0.1:8080
- Validator signing 100% of blocks
- Block rewards accumulating (currently going to placeholder `0x1111...` — change `CHAKRAM_REWARD_ADDRESS` in `docker/.env` to redirect)

---

## Known Limitations / Next Steps

| Item | Notes |
|---|---|
| Faucet endpoint broken | `eth_sendTransaction` with `from` requires an unlocked account; geth v1.17.6 removed account unlocking. Needs rewrite to use `eth_sendRawTransaction` with a pre-signed tx from the Hardhat key. |
| Reward address is placeholder | Change `CHAKRAM_REWARD_ADDRESS=0x1111...` in `docker/.env` to a real wallet address |
| `test_runtime.sh` not yet run end-to-end | The runtime test suite (Docker + live containers) was written but not fully validated against this fixed setup |
| Monitoring not tested | Prometheus + Grafana stack not started in this session |
