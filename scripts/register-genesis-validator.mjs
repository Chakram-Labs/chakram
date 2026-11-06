#!/usr/bin/env node
// Registers the genesis validator on the StakingRegistry.
// Whitelists the EVM address (owner-only), then calls register() with
// the node's Ed25519 pubkey and a chosen reward address.
//
// Usage:
//   node register-genesis-validator.mjs

import { ethers } from '/Users/anandhusatheesh/chakram3/web/node_modules/ethers/lib.esm/index.js';

// ── Config ────────────────────────────────────────────────────────────────────

const RPC_URL           = 'https://rpc.chakram.one';
const STAKING_CONTRACT  = '0x0eD064CA59AbCBe5f17202dAF4b23358608CF19b';

// Hardhat #0 — owner of the StakingRegistry (has 1B CHK in genesis)
const OWNER_PRIVATE_KEY = '0x660ccdb3b1bd27d0a7f8b1f0cb9da745bb9151ddec259a85515944162d2965d7';

// The node's Ed25519 pubkey (base64 from priv_validator_key.json)
const ED25519_PUBKEY_B64 = 'l04y7fiXpgDTZGBmq4DNNrzgLpvJGs2AA0+tnuAOYl0=';

// Where block rewards go — the generated EVM address (same as contract owner)
const REWARD_ADDRESS = '0xdeF22dBC999aD395527205C90d7922357378CAe0';

// ── ABI (minimal) ─────────────────────────────────────────────────────────────

const ABI = [
  'function whitelist(address validator)',
  'function register(bytes32 ed25519PubKey, address rewardAddress) payable',
  'function isActive(address) view returns (bool)',
  'function minStake() view returns (uint256)',
  'function validators(address) view returns (bytes32, address, uint256, bool, bool, bool, bool, uint16, uint256)',
];

// ── Main ──────────────────────────────────────────────────────────────────────

const provider = new ethers.JsonRpcProvider(RPC_URL);
const wallet   = new ethers.Wallet(OWNER_PRIVATE_KEY, provider);
const contract = new ethers.Contract(STAKING_CONTRACT, ABI, wallet);

console.log('Owner address:', wallet.address);
console.log('Staking contract:', STAKING_CONTRACT);

// Convert base64 Ed25519 pubkey → bytes32 hex
const pubKeyBytes = Buffer.from(ED25519_PUBKEY_B64, 'base64');
if (pubKeyBytes.length !== 32) {
  console.error('ERROR: pubkey must be 32 bytes, got', pubKeyBytes.length);
  process.exit(1);
}
const pubKeyHex = '0x' + pubKeyBytes.toString('hex');
console.log('Ed25519 pubkey (hex):', pubKeyHex);
console.log('Reward address:', REWARD_ADDRESS);

// Check minStake
const minStake = await contract.minStake();
console.log('Min stake:', ethers.formatEther(minStake), 'CHK');

// Check if already active
const alreadyActive = await contract.isActive(wallet.address);
if (alreadyActive) {
  console.log('✓ Validator already active — nothing to do.');
  process.exit(0);
}

// Step 1: whitelist
console.log('\nStep 1: whitelisting', wallet.address, '...');
const tx1 = await contract.whitelist(wallet.address);
console.log('  tx:', tx1.hash);
await tx1.wait();
console.log('  ✓ whitelisted');

// Step 2: register
console.log('\nStep 2: registering validator ...');
const tx2 = await contract.register(pubKeyHex, REWARD_ADDRESS, { value: minStake });
console.log('  tx:', tx2.hash);
await tx2.wait();
console.log('  ✓ registered');

// Verify
const active = await contract.isActive(wallet.address);
console.log('\nValidator active:', active);
if (active) {
  console.log('\n✓ Done! Genesis validator registered.');
  console.log('  Next block finalized by this node will emit EventRegistered');
  console.log('  → CometBFT ValidatorUpdate → rewards route to', REWARD_ADDRESS);
} else {
  console.log('✗ Not active — check contract state.');
}
