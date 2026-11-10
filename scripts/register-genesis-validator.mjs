#!/usr/bin/env node
// Registers the genesis validator on the StakingRegistry.
// Whitelists the EVM address (owner-only), then calls register() with
// the node's Ed25519 pubkey and a chosen reward address.
//
// Usage:
//   OWNER_PRIVATE_KEY=0x... node register-genesis-validator.mjs
//
// Optional env overrides:
//   EVM_RPC_URL, NODE_API_URL, STAKING_CONTRACT, REWARD_ADDRESS
//
// Requires ethers v6: npm install ethers

import { ethers } from 'ethers';

// ── Config ────────────────────────────────────────────────────────────────────

const EVM_RPC_URL       = process.env.EVM_RPC_URL      || 'https://rpc.chakram.one';
const NODE_API_URL      = process.env.NODE_API_URL     || 'https://node.chakram.one';
const STAKING_CONTRACT  = process.env.STAKING_CONTRACT || '0x532119646393a09c1CfA31FC16CC995917581647';

const OWNER_PRIVATE_KEY = process.env.OWNER_PRIVATE_KEY;
if (!OWNER_PRIVATE_KEY) {
  console.error('Error: OWNER_PRIVATE_KEY environment variable is required');
  process.exit(1);
}

// ── ABI (minimal) ─────────────────────────────────────────────────────────────

const ABI = [
  'function whitelist(address validator)',
  'function register(bytes32 ed25519PubKey, address rewardAddress) payable',
  'function isActive(address) view returns (bool)',
  'function minStake() view returns (uint256)',
  'function validators(address) view returns (bytes32, address, uint256, bool, bool, bool, bool, uint16, uint256)',
];

// ── Fetch current validator pubkey from CometBFT ──────────────────────────────

async function fetchValidatorPubkey() {
  const url = NODE_API_URL + '/api/node-info';
  try {
    const res  = await fetch(url);
    const data = await res.json();
    const pubkey = data?.validator_pubkey;
    if (!pubkey) throw new Error('validator_pubkey missing from node-info response');
    console.log('Fetched validator pubkey from node-info:', pubkey);
    return pubkey;
  } catch (e) {
    throw new Error(
      `Could not fetch validator pubkey from ${url}: ${e.message}\n` +
      `  Fallback: ssh <your-vm> "python3 -c \\"import json; d=json.load(open('/root/.chakram-node/comet/config/priv_validator_key.json')); print(d['pub_key']['value'])\\""`,
    );
  }
}

// ── Main ──────────────────────────────────────────────────────────────────────

const ED25519_PUBKEY_B64 = await fetchValidatorPubkey();

const provider = new ethers.JsonRpcProvider(EVM_RPC_URL);
const wallet   = new ethers.Wallet(OWNER_PRIVATE_KEY, provider);
// Default to owner wallet if not set (fresh-chain.sh auto-detects the genesis node's EVM wallet)
const REWARD_ADDRESS = process.env.REWARD_ADDRESS || wallet.address;
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

const GENESIS_STAKE = ethers.parseEther('50'); // genesis validator stakes 50 CHK
if (GENESIS_STAKE < minStake) {
  console.error('ERROR: GENESIS_STAKE (50 CHK) is below contract minStake');
  process.exit(1);
}

// Check if already active
const alreadyActive = await contract.isActive(wallet.address);
if (alreadyActive) {
  console.log('Validator already active — nothing to do.');
  process.exit(0);
}

// Step 1: whitelist
console.log('\nStep 1: whitelisting', wallet.address, '...');
const tx1 = await contract.whitelist(wallet.address);
console.log('  tx:', tx1.hash);
await tx1.wait();
console.log('  whitelisted');

// Step 2: register
console.log('\nStep 2: registering validator with 50 CHK stake ...');
const tx2 = await contract.register(pubKeyHex, REWARD_ADDRESS, { value: GENESIS_STAKE });
console.log('  tx:', tx2.hash);
await tx2.wait();
console.log('  registered');

// Verify
const active = await contract.isActive(wallet.address);
console.log('\nValidator active:', active);
if (active) {
  console.log('\nDone! Genesis validator registered.');
  console.log('  Next block finalized by this node will emit EventRegistered');
  console.log('  Rewards route to', REWARD_ADDRESS);
} else {
  console.log('Not active — check contract state.');
}
