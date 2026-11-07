# Validator UX — Pre-Mainnet Requirement

**Status:** Planned  
**Priority:** Must-have before mainnet  
**Effort:** ~3–4 days  

## Problem

A new user downloads `chakram-node`, syncs, opens the dashboard and sees:

- Validator page: "not in set, inactive", an address, a public key, voting power 0
- No explanation of what any of these mean
- No EVM wallet — the node uses `0x000...000` as reward address unless `--reward` is passed manually
- Wallet page is empty/useless
- No way to export a key or know what address to tell the chain owner to whitelist

The result: the user has no idea what their EVM address is, what the CometBFT address is, or how the two relate. They cannot register as a validator without external help.

---

## What needs to be built

### 1. Auto-generate EVM wallet on first run

In `cmd/chakram-node/commands.go`, `resolveRewardAddr()` currently falls back to `0x000...000`. It should instead:

1. Check if `~/.chakram-node/reward-key.json` exists — if yes, load it
2. If not, generate a new secp256k1 keypair using `crypto/ecdsa` + `github.com/ethereum/go-ethereum/crypto`
3. Save it to `reward-key.json` as `{ "address": "0x...", "private_key": "0x..." }`
4. Print the address on first run so the user sees it immediately

```go
// reward-key.json format
{
  "address":     "0xABCD...1234",
  "private_key": "0x..."         // hex, no 0x prefix also acceptable
}
```

The reward address is then passed automatically to `startConsensus` — no `--reward` flag needed.

### 2. Export private key via dashboard

The Wallet page (`/wallet`) must show:

- The node's EVM address (from `reward-key.json`)
- CHK balance (read from geth RPC)
- A "Show Private Key" button — click reveals the key with a warning, copy button
- A "Copy Address" button
- Faucet button (if faucet configured on the network)
- Clear label: **"This is your node's EVM wallet. Import this key into MetaMask to register as a validator."**

The private key must be served via a local-only API endpoint — only accessible from `localhost`, never exposed through the nginx reverse proxy.

### 3. API endpoint for node wallet

New endpoint: `GET /api/node-wallet`  
Returns:
```json
{
  "address":     "0xABCD...1234",
  "balance_wei": "1000000000000000000",
  "has_key":     true
}
```

Private key is **never** returned via API. Export is UI-only (read from disk on the Go side, displayed once in-browser).

For the private key export specifically: `GET /api/node-wallet/export-key` — only callable from `127.0.0.1`, returns `{ "private_key": "0x..." }`. The API server must reject this request if the caller is not localhost.

### 4. Rewrite Validator page

Split clearly into two sections:

**Section A — Consensus Identity (CometBFT)**
- Label: "CometBFT Validator Key"
- Address (hex) — with tooltip: "Used by CometBFT for block signing. Not an EVM address."
- Public key (base64) — with tooltip: "Submitted to the staking contract during registration."
- Voting power — with explanation: "0 means not yet registered. Becomes 10 after registration."
- Status chip: "Not registered" / "Active" / "Jailed"

**Section B — EVM Wallet**
- Label: "Node EVM Wallet"  
- Address — with tooltip: "Your Ethereum-compatible address. This is what gets whitelisted."
- CHK balance
- Link to Wallet page for key export
- Explanation: "The chain owner must whitelist this address before you can register."

### 5. Rewrite Wallet page

Currently shows a generic address lookup. Replace with two tabs:

**Tab 1 — Node Wallet**  
The node's own auto-generated EVM wallet (see §2 above).

**Tab 2 — Address Lookup**  
The existing address search functionality.

### 6. Remove Connect Wallet from dashboard

The dashboard is a local node management tool (`localhost:8080`), not a dApp. The node operator IS the node. Once the node has its own EVM wallet (§1), all signing (register, unbond, withdraw) is done by the node itself via internal API calls — no MetaMask, no RainbowKit needed.

**Remove from the UI:**
- "Connect Wallet" button on Staking page
- WalletBar component
- All `window.ethereum` / `connectWallet()` calls in `Staking.tsx` and `Settings.tsx`
- `connectWallet`, `ensureChakramNetwork`, `getEthProvider` from `staking.ts`

**Replace with:**
- Staking page reads the node's own wallet address from `/api/node-wallet`
- Register / Unbond / Withdraw buttons POST to local API endpoints that sign with the node's key
- New API endpoints: `POST /api/staking/register`, `POST /api/staking/unbond`, `POST /api/staking/withdraw`

The only external wallet interaction (owner calling `whitelist()`) is handled by `contracts/whitelist.py` — not the UI.

---

## Registration flow after these changes

1. User runs `chakram-node` → EVM wallet auto-generated, address printed to terminal
2. User opens `localhost:8080` → Validator page shows both keys clearly
3. User copies their EVM address → sends to chain owner
4. Owner runs: `python3 contracts/whitelist.py --address 0xUserAddress`
5. User opens Staking page → connects MetaMask (imported from Wallet page export)
6. Clicks Register → tx confirmed → next block they are active validator

---

## Notes

- Private key file permissions must be `0600` (owner read-only)
- The export-key API endpoint must check `r.RemoteAddr` starts with `127.0.0.1` before returning the key
- On Windows `%APPDATA%\chakram-node\reward-key.json`
- Do not store the private key in the Go process memory longer than needed for the export request
- The node's EVM wallet is a "hot wallet" for rewards only — users should understand it is not a custody wallet and they should back up the key
