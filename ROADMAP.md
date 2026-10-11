# Chakram 2.0 Roadmap

> Named after the silver coin of Travancore. A fair-launch chain anyone can mine, with full smart contracts.

## Vision

Chakram 2.0 is a fresh start: a proof-of-work blockchain secured by RandomX, running the Ethereum Virtual Machine. Anyone can mine it on a regular computer, nobody gets coins before launch, and every Ethereum tool (MetaMask, Solidity, Hardhat, Uniswap-style apps) works on day one.

## Principles

- **Fair launch.** No premine, no presale, no insider allocation. Coins only come from mining.
- **Nobody owns it.** Open source, open to any miner, governed by the people who run it.
- **Ethereum-compatible.** Builders bring their existing code and tools.
- **Secure before fast.** Every consensus rule is tested, fuzzed and reviewed before mainnet.
- **Build in public.** Progress, decisions and testnets are shared openly.

## What 2.0 is

| Layer | Choice |
|---|---|
| Consensus | Chakram's own Go code: RandomX PoW, LWMA difficulty, median-time-past, chainwork fork choice |
| Execution | Unmodified geth, driven through the Engine API |
| Block time | 15 seconds |
| Rewards | Paid to miners through Engine API withdrawals |
| Currency | CHK, 18 decimals |
| Launch | Fresh genesis. v1.x stays private as a reference |

The model: Chakram works like Ethereum's beacon chain, but blocks are sealed with RandomX proof-of-work instead of staking.

## Native features (system contracts at genesis)

1. **Mine your gas.** New users earn their first gas by mining, with no exchange, KYC or bank needed.
2. **No-clone token registry.** The first token to claim a ticker owns it; copies are permanently flagged. Optional fair-launch rules limit snipers.
3. **Proof stamps.** Cheap, permanent timestamps and signatures for documents, certificates and media, verifiable by anyone.

## Security priorities

- Protect against rented or redirected RandomX hashpower (Monero, NiceHash) through custom RandomX parameters and/or merge-mining.
- Checkpoints and periodic anchoring of Chakram state to Bitcoin.
- Bounded reorg depth with finality hints to geth.

## Later

- Bridged USDC/USDT for stable payments.
- An INR stablecoin, only with a licensed partner.
- A possible move to proof-of-stake for 1–2 second finality, with validators drawn from the people who mined.

## Milestones

1. Decisions and architecture spec
2. Single-node devnet: first Solidity contract deployed on Chakram
3. Multi-node devnet: sync, reorgs and crash recovery proven
4. Security hardening
5. Native features
6. Tooling: explorer, faucet, docs, mining guide
7. Public testnet
8. Audit and mainnet launch

Details for each phase are in [PHASES.md](./PHASES.md).
