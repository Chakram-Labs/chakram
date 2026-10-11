# Chakram 2.0 (CHK)

**Website:** https://chakram.one · **Explorer:** https://chakram.one · **Download:** https://chakram.one/download

> ചക്രം — The heritage of Kerala, reborn in the digital age.

Chakram 2.0 is a CPU-mineable EVM-compatible blockchain built on a custom RandomX proof-of-work consensus layer backed by go-ethereum as the execution engine. Not a fork — every line of the consensus layer is original.

> **v1 is archived.** The v1 UTXO node is preserved on the `legacy-v1` branch and tagged `v1.1.0-final`. All active development is on this branch (2.0 only).

## Architecture

| Layer | Component |
|---|---|
| Execution | go-ethereum (geth v1.17+), Engine API V3 |
| Consensus | `cmd/chakram` — RandomX PoW, LWMA-3 difficulty, EIP-4895 rewards |
| Consensus lib | `consensus/` — block format, difficulty, genesis, store (BadgerDB) |
| Execution client | `engine/` — Engine API JSON-RPC client with JWT auth |

## Quick Start

### One-time setup

```bash
# Initialize geth with the network genesis (shown for testnet)
geth init --datadir <datadir> config/genesis.testnet.json
openssl rand -hex 32 > <datadir>/jwt.hex
```

### Start geth (terminal 1)

```bash
geth --datadir <datadir> \
  --syncmode full \
  --authrpc.addr 127.0.0.1 --authrpc.port 8551 \
  --authrpc.jwtsecret <datadir>/jwt.hex \
  --http --http.addr 127.0.0.1 --http.port 8545 --http.api eth,net,web3 \
  --nodiscover --maxpeers 0
```

### Start the consensus node (terminal 2)

```bash
./chakram --network testnet \
  --datadir <datadir> \
  --jwt <datadir>/jwt.hex \
  --miner 0xYourAddress
```

The `--network` flag selects genesis, chain ID and initial difficulty:

| Flag value | Chain ID | Use case |
|---|---|---|
| `devnet` | 11641 | Local single-node development |
| `testnet` | 11640 | Public testnet |
| `mainnet` | 1164 | Production mainnet |

### Mining options

```bash
--threads 4          # use 4 CPU threads (default: NumCPU-1)
--light              # RandomX light mode (~256 MB RAM, ~1/10 hashrate)
--no-mine            # relay/sync node only
```

## Build from Source

Requires Go 1.21+ and a C compiler (for RandomX CGo bindings).

```bash
git clone https://github.com/anandhu-here/chakram
cd chakram

# Native build (CGo enabled — full RandomX performance, ~1,076 H/s on M1)
go build -o chakram ./cmd/chakram

# Pure-Go build (relay nodes; no mining)
CGO_ENABLED=0 go build -o chakram ./cmd/chakram
```

## Tests

```bash
go test ./...                          # unit tests
go test ./... -race                    # with race detector
go test -tags hashrate ./cmd/chakram   # full-mode hashrate + key-switch test
go test -tags integration ./consensus  # requires geth on PATH
```

## Networks

| | Testnet | Mainnet |
|---|---|---|
| Chain ID | 11640 | 1164 |
| Block time | ~15 sec | ~15 sec |
| Halving | every 2,102,400 blocks (~1 year) | every 2,102,400 blocks |
| Initial reward | 5 CHK | 5 CHK |
| Algorithm | RandomX full mode | RandomX full mode |
| Epoch length | 2,048 blocks (~8.5 h) | 2,048 blocks (~8.5 h) |

## Data directory layout

```
<datadir>/
  jwt.hex         — JWT secret shared with geth
  consensus/      — BadgerDB: consensus blocks and tip pointer
  geth/           — geth execution state (managed by geth)
```

## RPC

The execution layer JSON-RPC is served by geth on `--http.port` (default 8545).

## About

The **Chakram** (ചക്രം) was a small copper coin used in the Travancore kingdom of Kerala from the 18th–19th centuries. The word **"cash"** itself derives from the Malayalam **"Kasu"** (കാശ്). Chakram (CHK) honours that heritage.

## License

MIT
