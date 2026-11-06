# Chakram Node Deployment Guide

This guide covers running a Chakram node on a fresh Linux VM — either as the **seed/operator node** or as a **validator joining the network**.

---

## Requirements

| Resource | Minimum | Recommended |
|---|---|---|
| CPU | 2 vCPU | 2 vCPU |
| RAM | 4 GB | 8 GB |
| Disk | 30 GB SSD | 50 GB SSD |
| OS | Ubuntu 22.04 | Ubuntu 24.04 LTS |
| Ports open | 22 (SSH), 8080 (dashboard), 26656 (p2p), 30303 (geth p2p) | same |

**GCP recommended VM:** `e2-standard-2` in `asia-south1-c` (~$55/month with 50 GB balanced SSD).

---

## 1. Prepare the VM

### 1a. Create the VM (GCP example)

In the GCP console → Compute Engine → Create Instance:
- **Name:** `chakram-node`
- **Region:** asia-south1 (Mumbai)
- **Machine type:** e2-standard-2
- **Boot disk:** Ubuntu 24.04 LTS, 50 GB Balanced persistent disk
- **Firewall:** leave HTTP/HTTPS off (we use custom ports)

### 1b. Open firewall ports

```bash
# Dashboard (public)
gcloud compute firewall-rules create allow-chakram-dashboard \
  --direction=INGRESS --action=ALLOW \
  --rules=tcp:8080 --source-ranges=0.0.0.0/0 \
  --target-tags=chakram-node --project=YOUR_PROJECT

# CometBFT p2p (validators need this)
gcloud compute firewall-rules create allow-chakram-p2p \
  --direction=INGRESS --action=ALLOW \
  --rules=tcp:26656 --source-ranges=0.0.0.0/0 \
  --target-tags=chakram-node --project=YOUR_PROJECT

# Geth p2p
gcloud compute firewall-rules create allow-geth-devp2p \
  --direction=INGRESS --action=ALLOW \
  --rules=tcp:30303,udp:30303 --source-ranges=0.0.0.0/0 \
  --target-tags=chakram-node --project=YOUR_PROJECT
```

Then add the network tag `chakram-node` to your VM instance.

> Port 8545 (Geth RPC) must **not** be opened publicly — it has no authentication.

### 1c. SSH into the VM

```bash
ssh user@VM_IP
```

---

## 2. Get the Code

```bash
# Install git if not present
sudo apt-get update && sudo apt-get install -y git

# Clone
git clone https://github.com/Chakram-Labs/chakram.git ~/chakram3
cd ~/chakram3
```

---

## 3. Run the Deploy Script

The script installs Docker, installs Go, builds `chakramctl`, inits the node, and starts everything.

### Seed node (operator)

```bash
bash scripts/deploy.sh \
  --mode dev \
  --reward-addr 0xYOUR_EVM_ADDRESS
```

### Validator joining the network

```bash
bash scripts/deploy.sh \
  --mode fullnode \
  --reward-addr 0xYOUR_EVM_ADDRESS \
  --peers "NODE_ID@SEED_IP:26656"
```

> **Where to get `NODE_ID`:** Ask the network operator. They can find it by running `docker compose logs chakram | grep "NodeID"` or visiting `http://SEED_IP:8080` and checking the Node Info panel.

### Script options

| Flag | Default | Description |
|---|---|---|
| `--mode` | `dev` | `dev` = seed/solo node · `fullnode` = join live network |
| `--reward-addr` | _(empty)_ | EVM address that receives 5 CHK block rewards |
| `--peers` | _(empty)_ | CometBFT peer string (required for fullnode) |
| `--chain-id` | `chakram-devnet-1` | Override chain ID |
| `--rebuild` | false | Force Docker image rebuild |

---

## 4. Verify

```bash
# Check containers are up
docker compose --profile dev ps   # or --profile fullnode

# Tail logs
docker compose --profile dev logs -f

# Quick health check
curl -s http://localhost:26657/status | python3 -m json.tool | grep -E "latest_block_height|catching_up"
```

Dashboard is live at `http://VM_IP:8080`.

---

## 5. Register as a Validator

Once your node is synced and the network operator has whitelisted your address:

1. Open the dashboard at `http://VM_IP:8080`
2. Go to **Settings** → set Geth RPC to `http://localhost:8545` (or the seed node's RPC if you're on a fullnode)
3. Go to **Staking**
4. In the **Validator Panel**, enter your private key
5. Your Ed25519 pubkey fills in automatically from the running node
6. Set your reward address and stake amount (minimum: 1 CHK)
7. Click **Register** — this submits the on-chain transaction

Your validator will appear in CometBFT's active set within one block.

---

## 6. Day-to-Day Operations

```bash
cd ~/chakram3/docker

# Stop
docker compose --profile dev down

# Start
docker compose --profile dev up -d

# Restart one service
docker compose --profile dev restart chakram

# View logs
docker compose --profile dev logs chakram -f
docker compose --profile dev logs geth-dev -f

# Full rebuild (after code update)
git pull
docker compose --profile dev up -d --build
```

---

## 7. Updating the Node

```bash
cd ~/chakram3
git pull
cd docker
docker compose --profile dev up -d --build
```

The deploy script is idempotent — running it again after a `git pull` rebuilds and restarts cleanly without re-generating genesis or keys.

---

## 8. Backup

Critical files to back up:

| File | Contains |
|---|---|
| `docker/secrets/jwt.hex` | Engine API JWT — both geth and chakram need this to match |
| `docker/devnet/config/priv_validator_key.json` | CometBFT validator signing key — **loss = cannot produce blocks** |
| `docker/devnet/config/node_key.json` | Node identity key |
| `docker/.env` | All configuration |

```bash
# Quick backup
tar czf ~/chakram-backup-$(date +%Y%m%d).tar.gz \
  ~/chakram3/docker/secrets/ \
  ~/chakram3/docker/devnet/config/ \
  ~/chakram3/docker/.env
```

---

## Ports Reference

| Port | Protocol | Exposed to | Purpose |
|---|---|---|---|
| 8080 | TCP | Public | Dashboard UI |
| 26656 | TCP | Public | CometBFT p2p |
| 26657 | TCP | Localhost | CometBFT RPC |
| 30303 | TCP+UDP | Public | Geth p2p discovery |
| 8545 | TCP | **Localhost only** | Geth JSON-RPC (no auth) |
| 8546 | TCP | Localhost | Geth WebSocket |
| 8551 | TCP | Localhost | Engine API (JWT protected) |

---

## Troubleshooting

**Node stuck / no blocks:**
```bash
docker compose --profile dev logs chakram --tail=50
# Look for: "geth unreachable" or "PrepareProposal error"
```

**Geth not starting:**
```bash
docker compose --profile dev logs geth-dev --tail=50
# Common: JWT secret mismatch — re-run setup.sh
```

**Out of disk:**
```bash
df -h /
# Clean Docker build cache:
docker builder prune -af
```

**Re-initialise from scratch:**
```bash
cd ~/chakram3/docker
docker compose --profile dev down -v   # removes volumes
rm .env secrets/jwt.hex
bash scripts/deploy.sh --mode dev --reward-addr 0xYOUR_ADDRESS
```
