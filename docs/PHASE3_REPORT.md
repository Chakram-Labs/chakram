# Chakram Phase 3 Close-out Report

> Date: 2026-11-05 → 2026-11-06
> Chain: chakram-devnet (single-node, localhost)
> Stack: CometBFT v0.40.0 · geth v1.17.6 · Go 1.27 · Solidity 0.8.36 · React 19 · ethers.js v6
> Branch: master

---

## Summary

Phase 3 delivered on-chain permissioned staking for Chakram. A `StakingRegistry.sol` contract was deployed to the live devnet, the Go API was extended with a live staking endpoint, and the web dashboard gained a fully interactive Staking page — replacing all Python scripts with browser-side transaction forms. Block rewards via EIP-4895 withdrawals (5 CHK/block) were confirmed working end-to-end.

The phase was then extended (2026-11-06) with a major contract upgrade (v2), Docker hardening for production readiness, a MetaMask integration, and a tokenomics/architecture discussion that settled the decentralisation roadmap.

---

## Part 1 — Core Phase 3 Delivery

### 1. EIP-4895 Withdrawals — Block Rewards

**Problem:** geth was rejecting blocks with `"unexpected withdrawals before shanghai"`. The genesis had no Shanghai/Cancun config so withdrawals were forbidden at the protocol level.

**Fix:** `docker/setup.sh` now generates the geth genesis with `shanghaiTime: 0` and `cancunTime: 0`, activating both forks from block 0. `chakramctl init` was fixed to find `docker/setup.sh` (not just `cwd/setup.sh`) and to write the `.env` to `docker/.env` where compose reads it.

**Result:** Withdrawals land every block. Each validator receives 5 CHK per block at the configured reward address.

---

### 2. StakingRegistry.sol v1 — Permissioned PoS Contract

**Location:** `contracts/StakingRegistry.sol`

The owner → whitelist → register → CometBFT power loop:

| Function | Who calls | Effect |
|---|---|---|
| `whitelist(addr)` | Owner | Marks an EVM address as allowed to register |
| `removeWhitelist(addr)` | Owner | Revokes whitelist |
| `register(ed25519PubKey, rewardAddr)` payable | Whitelisted validator | Stakes CHK, sets CometBFT pubkey and reward address |
| `jail(addr)` | Owner | Removes from active set, sets jailed flag |
| `unjail(addr)` | Owner | Clears jailed flag, restores to active set |
| `startUnbonding()` | Active validator | Begins unbonding countdown, removes from active set |
| `withdraw()` | Unbonding validator | Returns stake after unbonding period |

**Deployment details (devnet v1):**
- Address: `0x5FbDB2315678afecb367f032d93F642f64180aa3`
- Chain ID: 11642
- Min stake: 1 CHK · Unbonding: 60s
- Owner: Hardhat #0 (`0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266`)
- Validator confirmed active at block 472, power=1

---

### 3. GET /api/staking — Live Contract State

**New file:** `api/handler_staking.go`

Raw `eth_call` via `golang.org/x/crypto/sha3` + stdlib ABI encoding (no go-ethereum dependency). Joins CometBFT voting power by matching Ed25519 pubkey → CometBFT address (SHA256(pubKey32)[0:20]).

---

### 4. Staking Dashboard Page

**File:** `web/src/pages/Staking.tsx`

- Stat cards: Active Validators · Total Staked · Unbonding Period
- Contract info panel (polled every 10s)
- Validators table: address · stake · status badge · voting power · reward address
- **Owner Panel**: private key input (show/hide), derives address, Whitelist + Unjail tx forms
- **Validator Panel**: auto-fills Ed25519 pubkey from `/api/validator`, Register + Unbond + Withdraw tx forms with correct enabled/disabled state

**Key library:** `web/src/lib/staking.ts` — ethers.js v6. Private keys never leave the browser.

---

### 5. ETH → CHK Labels

Fixed across Wallet, Validator, and Staking pages.

---

### 6. Settings — Geth RPC URL

`web/src/pages/Settings.tsx` now has a Geth RPC URL field (default `http://127.0.0.1:8545`) saved to `localStorage.chakram_geth_rpc`. Staking forms read this for direct transaction submission.

---

### 7. Bugs Fixed

| Bug | Root cause | Fix |
|---|---|---|
| `unexpected withdrawals before shanghai` | geth genesis had no Shanghai/Cancun config | `setup.sh` sets `shanghaiTime:0, cancunTime:0` |
| `chakramctl init` couldn't find setup.sh | Looked only in cwd | Added `docker/setup.sh` fallback in `cmd_init.go` |
| `CHAKRAM_MODE=fullnode` in .env | Wrong profile name | Changed to `dev` |
| Solidity em-dash compile error | `solc` rejects non-ASCII | Replaced `—` with `-` |
| `web3.py InvalidAddress` | Reward address not checksummed | `Web3.to_checksum_address()` |
| Staking endpoint 404 | Old binary in running container | Rebuilt with `--build` |

---

## Part 2 — Phase 3 Extended (2026-11-06)

### 8. StakingRegistry.sol v2 — Full Production Contract

Complete rewrite of the contract with four major additions. **Compiles clean with solc 0.8.36, zero warnings.**

#### 8a. Automatic Decentralisation (immutable, encoded in bytecode)

The whitelist requirement drops permanently when **any** of three independent conditions is met. The owner has no power to delay or block any trigger:

| Trigger | Condition | Rationale |
|---|---|---|
| Time | 18 months after deploy (`548 days`, `immutable`) | Enough time for network maturation and regulatory standing |
| Validators | 20 active validators (`immutable`) | 20 institutions = genuinely decentralised; 2/3 = 13-14 for BFT |
| Vote | 66% of active stake votes YES within 7 days | Validators can accelerate if network is ready early |

```solidity
function isPermissionless() public view returns (bool) {
    return openedEarly
        || block.timestamp      >= permissionlessAfter  // 18 months
        || activeValidatorCount >= validatorThreshold;  // 20 validators
}
```

`register()` skips the whitelist check as soon as `isPermissionless()` returns true. No redeployment needed — the transition is automatic.

**Governance vote mechanics:**
- Any active validator calls `proposeEarlyOpen()` — 30-day cooldown between proposals
- Active validators call `voteEarlyOpen()` — weight = their stake
- If 66% of `totalActiveStake` votes YES → `openedEarly = true` immediately
- Failed proposals expire via `expireProposal()` (anyone can call after deadline)
- `proposalNonce` prevents replay votes across proposals

#### 8b. Slashing (callable by ABCI app without redeploy)

```solidity
function slash(address validator, uint256 bps) external  // caller must be slasher
function setSlasher(address _slasher) external onlyOwner  // set to ABCI app address
function setTreasury(address _treasury) external onlyOwner // address(0) = burn
```

- Active validator → jailed, removed from set, stake reduced by `bps/10000`
- Unbonding validator → jailed, unbonding cancelled (can't escape before review)
- Already jailed → stake reduced only

**Recommended bps by offence:**
- Liveness fault (too many missed blocks): **100 bps (1%)**
- Double-sign: **10000 bps (100%)**

When the ABCI app detects evidence (Phase 4), it calls `slash()` directly — no contract change needed.

#### 8c. Commission Rate (read by future DelegationRegistry)

```solidity
// stored in ValidatorInfo struct, same storage slot as bools (gas efficient)
uint16 commissionBps;  // 0–10000

function setCommission(uint16 bps) external  // validator calls this
```

When `DelegationRegistry.sol` is deployed, it reads `validators(addr).commissionBps` to split delegation rewards between validator and delegators. No StakingRegistry redeploy needed.

#### 8d. Validator Metadata

```solidity
mapping(address => string) public monikers;  // "University of Delhi"
mapping(address => string) public websites;  // "https://du.ac.in"

function setMoniker(string calldata name) external  // 1-64 chars
function setWebsite(string calldata url) external   // max 128 chars
```

Displayed in the staking UI. Read by DelegationRegistry for delegator-facing validator selection.

#### 8e. New View Functions

```solidity
// Single call returns everything DelegationRegistry and UI need
function validatorProfile(address) external view returns (
    bool active, bool jailed, uint256 stake,
    uint16 commissionBps, string moniker, string website, address rewardAddress
)

// Live decentralisation progress — shown in Staking UI
function decentralisationStatus() external view returns (
    bool permissionless, uint256 openAt, uint256 threshold,
    uint256 activeCount, uint256 daysRemaining
)

// Live governance vote progress
function proposalProgress() external view returns (
    uint256 votesFor, uint256 totalStake, uint256 pct, uint256 deadline
)
```

#### 8f. Tracking State Added

- `activeValidatorCount` — incremented/decremented on register/jail/unjail/unbond
- `totalActiveStake` — sum of active validator stakes, used for vote weight calculation
- `registered` bool in ValidatorInfo — prevents duplicate entries in `validatorList` on re-register after withdraw

---

### 9. Docker Hardening — Production Ready

All issues fixed before the next build:

| File | Change | Why |
|---|---|---|
| `.dockerignore` (new) | Excludes `docker/secrets/`, `docker/.env`, `web/node_modules/`, `.git/`, `docs/`, `contracts/` | Secrets can no longer reach image layers; faster builds |
| `.gitignore` | Added `docker/.env` and `docker/secrets/` | Prevents accidental commit of JWT secret and Grafana password |
| `docker/.env` | Removed duplicate `CHAKRAM_PEERS=` entry | Was on lines 28 (empty) and 41 (with value) |
| `docker/.env.example` | Added `CHAKRAM_STAKING_CONTRACT=` with documentation | Phase 3 added the variable; example didn't document it |
| `docker-compose.yml` | geth healthcheck on all services (HTTP RPC ping) | Chakram now waits for geth to be ready (`condition: service_healthy`) instead of racing |
| `docker-compose.yml` | `stop_grace_period: 30s` on all chakram services | CometBFT flushes WAL on shutdown; default 10s SIGKILL was cutting it short |
| `docker-compose.yml` | `gcmode full` for fullnode and localnet geth | Archive mode was 3-5x storage overhead; only dev needs archive |
| `docker-compose.yml` | CORS note on geth-dev | `--http.corsdomain "*"` is dev-only; documented for production lockdown |

---

### 10. MetaMask "Add Chakram Network" Button

**File:** `web/src/pages/Overview.tsx`

Calls `wallet_addEthereumChain` with:
- `chainId: 0x2D7A` (11642)
- `chainName: Chakram`
- `nativeCurrency: { name: Chakram, symbol: CHK, decimals: 18 }`
- `rpcUrls`: read from Settings (`localStorage.chakram_geth_rpc`)
- `blockExplorerUrls`: read from Settings (`localStorage.chakram_api_url`)

If MetaMask is not detected, shows manual network details for any EVM wallet (Rabby, Frame, Coinbase Wallet, hardware wallets via MetaMask). Since Chakram is EVM-compatible, any EVM wallet works without custom integration.

---

## Tokenomics and Architecture Decisions

### CHK Value Without INRT

INRT requires RBI regulatory clearance and is not on the immediate roadmap. CHK does not need INRT to have value — the same way ETH had value for two years before USDT appeared on Ethereum. CHK is valuable because:

1. Deploying contracts on Chakram requires CHK for gas
2. Validators earn CHK for securing the network
3. Institutions building on Chakram (credentials, supply chain, identity) drive CHK demand

INRT is a future consumer layer on top of Chakram, not its foundation.

### Delegation — Why It's Phase 6+

Delegation (users stake CHK behind a validator without running a node) only makes sense when:
- CHK has real market value
- There are enough validators to choose from
- Regular users have CHK to delegate

The `commissionBps` field is in the contract now so `DelegationRegistry.sol` can be deployed without touching StakingRegistry. No redeploy needed when delegation is built.

### Mobile Nodes

PoS removes the GPU requirement, making phones theoretically capable of running nodes. In practice:
- **iOS**: hard no — Apple kills background processes
- **Android**: technically possible (gomobile), not worth engineering effort now
- **Best path**: phone users delegate CHK to institutional validators and earn rewards passively — no node required

### Modular Future Contracts

StakingRegistry is designed as the single source of truth for validator state. Future contracts read from it without needing write access:

```
StakingRegistry (deployed once, this contract)
├── validators(): registration, stake, commission, metadata
├── slash(): callable by ABCI app via slasher address
├── isPermissionless(): auto-transition state
└── clean read interface for all downstream contracts

DelegationRegistry (deploy when ready, Phase 6)
└── reads validators() for active/commission state
    tracks delegations independently

GovernanceRegistry (deploy when ready, Phase 6)
└── reads activeValidatorCount, totalActiveStake
    calls setMinStake(), transferOwnership() on StakingRegistry
```

---

## Onboarding Flow (How a New Validator Joins)

```
University admin contacts Chakram operator
          │
          ▼
Operator opens Staking page → Owner Panel
Enters owner private key → sees derived address (verifies it matches owner)
Enters admin's EVM address → clicks Whitelist
Tx confirmed on-chain
          │
          ▼
Admin starts their Chakram node
Admin opens Staking page → Validator Panel
Enters their private key → Ed25519 pubkey auto-fills from running node
Sets reward address, stake amount → clicks Register
Tx confirmed on-chain
Admin calls setMoniker("University of X") to appear in the validator list
          │
          ▼
CometBFT ABCI app reads staking contract on next PrepareProposal
Validator added to active set, power = stake / minStake
Block production continues with new validator participating
```

---

## Current State (End of Phase 3 Extended)

```
devnet block:       ~2000+  (running continuously)
geth height:        in sync with CometBFT
withdrawals:        5 CHK/block landing every block
contract v1:        deployed at 0x5FbDB2315678afecb367f032d93F642f64180aa3
contract v2:        written, compiled clean — needs redeploy to devnet
docker:             production-hardened (healthchecks, dockerignore, gcmode)
web UI:             MetaMask button, staking tx forms, CHK labels
validators:         1 registered, power=1, active
```

---

## Phase 4 — What's Next (Priority Order)

### 4.1 Redeploy StakingRegistry v2 (Immediate)

Deploy the new contract to devnet, update `CHAKRAM_STAKING_CONTRACT` in `.env`, rebuild containers. The v2 contract has the full decentralisation guarantee, slashing infrastructure, commission, and metadata — none of which exist on the currently deployed v1.

### 4.2 GCP Deployment — Public Node

- **VM:** e2-standard-2 (2 vCPU, 8 GB RAM, ~$50/month)
- **Disk:** 50 GB persistent SSD at `/data` (separate from boot disk, resizable)
- **Region:** asia-south1 (Mumbai) — lowest latency to India
- **Stack:** same Docker Compose, Nginx in front, HTTPS
- **Ports:** 26656 p2p public, 8080 dashboard public, 8545 behind VPN only
- **Total cost:** ~$55/month

### 4.3 Multi-Validator Consensus Test

Onboard a second real validator to prove:
- Full whitelist → register → active flow end-to-end with two parties
- CometBFT 2/3 majority consensus with multiple validators
- Jail/unjail with a live validator that goes offline
- Unbonding and withdrawal with real stake

### 4.4 Slashing ABCI Integration

Wire the ABCI app to call `slash()` automatically:
- Double-sign evidence in `ProcessProposal` → `slash(addr, 10000)` (100%)
- Liveness: N consecutive missed blocks → `slash(addr, 100)` (1%)
- Requires setting `slasher` to the ABCI app's signing address

### 4.5 Staking UI — Decentralisation Panel

Show live status on the Staking page:
- "Opens in X days OR when Y more validators join"
- Current vote progress if a proposal is active
- Vote button for active validators

### 4.6 INRT Stablecoin (Post-RBI Clearance)

INR-pegged ERC-20. Gated on Reserve Bank of India regulatory clearance. Not on the immediate roadmap. When cleared: `INRToken.sol`, oracle, gas sponsorship/Paymaster for end users, CHK/INRT DEX pool.

---

## Stack Reference

| Component | Version | Role |
|---|---|---|
| CometBFT | v0.40.0 | BFT consensus, block production |
| go-ethereum (geth) | v1.17.6 | EVM execution via Engine API |
| Go | 1.27 | ABCI app, API server, chakramctl |
| Solidity | 0.8.36 | StakingRegistry.sol v2 |
| React | 19 | Web dashboard |
| ethers.js | v6.17.0 | Browser-side tx signing |
| Tailwind CSS | v4 | UI styling |
| Docker Compose | v2 | Local devnet orchestration |

---

## Key Addresses (Devnet)

| Name | Value |
|---|---|
| StakingRegistry v1 (deployed) | `0x5FbDB2315678afecb367f032d93F642f64180aa3` |
| StakingRegistry v2 | needs redeploy |
| Owner (Hardhat #0) | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` |
| Validator reward addr | `0x20cdbb1a744809fd1544c786f4af8ded71c52314` |
| Chain ID | 11642 |
| Geth RPC | http://127.0.0.1:8545 |
| Dashboard | http://127.0.0.1:8080 |
