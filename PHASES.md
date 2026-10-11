# Chakram 2.0 Phases

Each phase lists its goal, scope, deliverables and the criteria for calling it done. Work one phase at a time; don't start the next until the current one meets its "Done when" list.

---

## Phase 0: Decisions and setup

**Goal:** lock the parameters everything else depends on.

**Decisions**
- Chain ID: unique, checked against chainlist.org
- Block time: 15 seconds (decided)
- Block reward and halving schedule, in 18-decimal units
- Base fee burn: keep geth's default burn, or not (changing it requires modifying geth)
- Gas limit (starting point: 30M)
- Finality depth for geth hints (starting point: 100 blocks, ~25 minutes)
- Maximum reorg depth
- RandomX: custom parameters, merge-mining with Monero, or both
- geth version to pin

**Setup**
- New repository layout: consensus client, geth config and genesis, scripts, docs
- GitHub org with at least two admins
- CI running build, vet, race tests and fuzzing

**Done when:** every decision above is written down in the repo, and CI is green on an empty skeleton.

---

## Phase 1: Architecture spec

**Goal:** a written spec precise enough to build from.

**Scope**
- Consensus block format: parent hash, height, timestamp, difficulty, nonce, and a commitment to the execution payload
- What RandomX hashes, and how the payload is committed in the header
- Engine API flow for mining: `forkchoiceUpdated` with payload attributes, `getPayload`, seal, `newPayload`, `forkchoiceUpdated`
- Engine API flow for receiving: cheap checks (PoW, difficulty, MTP) first, then `newPayload`, then fork choice
- Rewards through withdrawals: amount, recipient, units
- Payload attributes: timestamp rules, fee recipient, `prevRandao` (documented as miner-influenced, not safe randomness), `parentBeaconBlockRoot` set to the Chakram block hash
- Finality and reorg handling: safe/finalized hashes, reorg cap
- Payload refresh while mining, so new transactions aren't stuck
- Sync design: consensus sync over Chakram P2P, execution sync over geth's devp2p
- JWT authentication between the two processes
- Which v1.1 code is reused (RandomX, LWMA, MTP, P2P hardening, atomic storage) and which is dropped (UTXO, signatures, mempool)

**Done when:** the spec is in `docs/spec.md` and reviewed, with every open question answered.

---

## Phase 2: Single-node devnet

**Goal:** one machine where Chakram mines blocks and geth executes them.

**Scope**
- geth genesis: all Ethereum forks active at block 0, merged at genesis
- Consensus client driving geth through the Engine API
- RandomX sealing, LWMA difficulty at 15-second target
- Rewards via withdrawals
- Persistent consensus block store

**Done when**
- Blocks are mined about every 15 seconds and the miner's balance grows
- MetaMask connects and sends CHK between two accounts
- A Solidity contract is deployed and called successfully
- Restarting both processes resumes from the same head

---

## Phase 3: Multi-node devnet

**Goal:** prove the network behaves correctly with several nodes.

**Scope**
- 3+ nodes, at least two mining
- Block gossip, sync from genesis, sync of a late joiner
- Forced partitions and reorgs, including deeper reorgs up to the cap
- `kill -9` crash tests during sync and during reorgs
- Transactions sent through different nodes, including double-spend attempts
- Flag to disable seed nodes and peer discovery for private networks

**Done when**
- All nodes agree on the head hash after 100+ blocks
- Reorgs are handled correctly on both the consensus and geth sides
- Crashed nodes recover without manual repair
- Exactly one of any conflicting transactions confirms

---

## Phase 4: Security hardening

**Goal:** close the known risks before anyone else runs a node.

**Scope**
- Implement the chosen hashpower protection (custom RandomX parameters and/or merge-mining)
- Checkpoints and a design for anchoring Chakram state to Bitcoin
- Fuzz tests for every decoder that touches network input
- Race detector across the full test suite
- P2P hardening carried over from v1.1: deadlines, inbound/outbound slots, peer penalties, bounded dialing
- **RandomX verifier pool:** replace the single mutex-protected RandomX VM used for P2P block verification with a fixed-size pool (e.g. `runtime.NumCPU()` VMs). Concurrent block arrivals during IBD serialise behind one mutex today; a pool lets N blocks be verified in parallel, which will become the bottleneck once network propagation is faster than a single VM's throughput (~150–200 ms/block in light mode, ~500 ms in full mode).
- Internal review of the Engine API integration and consensus rules

**Done when:** fuzzing runs clean for extended periods, race tests pass repeatedly, and every finding from the internal review is fixed or explicitly accepted.

---

## Phase 5: Native features

**Goal:** the three features that make Chakram different, as system contracts at genesis.

**Scope**
- **No-clone token registry:** ticker ownership, clone flagging, optional fair-launch rules (e.g. max buy per wallet in early blocks)
- **Proof stamps:** stamp a hash with a signature and timestamp; verify by hash
- **Mine your gas:** design and build how a new user's mining effort turns into their first gas, with no prior balance

**Done when:** each feature has tests, documentation and a working demo on the multi-node devnet.

---

## Phase 6: Tooling

**Goal:** make Chakram easy to use, mine and build on.

**Scope**
- Block explorer (e.g. Blockscout)
- Testnet faucet
- MetaMask network config and "Add to wallet" button on chakram.one
- Mining guide for Mac, Windows and Linux, plus release binaries
- Developer docs: RPC endpoints, deploying contracts, native features
- Network dashboard: hashrate, blocks, miners

**Done when:** a new person can go from zero to mining, or to deploying a contract, using only the docs.

---

## Phase 7: Public testnet

**Goal:** real outside users, real bugs, early community.

**Scope**
- Seed nodes on 2–3 VPSes in different locations
- Public announcement and regular build-in-public updates
- Bug reporting through security@chakram.one, and a bug bounty if budget allows
- Testnet resets as needed

**Done when:** the testnet runs stably for several weeks with outside miners, and no critical bugs remain open.

---

## Phase 8: Audit and mainnet launch

**Goal:** a safe, fair launch.

**Scope**
- External audit of consensus code, Engine API integration and system contracts
- Fix all audit findings
- Final genesis, announced in advance with a published launch time
- Release binaries and seed nodes ready before launch
- Launch post: what Chakram is, how to mine, no premine

**Done when:** mainnet is live, blocks are being mined by independent miners, and the explorer and docs point to mainnet.

---

## After launch

- Bridged USDC/USDT
- INR stablecoin with a licensed partner
- Bitcoin anchoring live
- Research and plan for a proof-of-stake transition
