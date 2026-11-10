export interface StatusResponse {
  node_id: string; moniker: string; chain_id: string; height: number;
  latest_block_hash: string; latest_app_hash: string; catching_up: boolean;
  peers: number; comet_version: string; validator_address?: string; voting_power?: number;
  geth_height: number; geth_finalized: number; geth_available: boolean; health_gate_blocked: boolean;
  max_peer_height: number;
}
export interface ValidatorResponse {
  address: string; pub_key: string; voting_power: number; is_active: boolean;
  signed_last_100: number; missed_last_100: number; reward_address?: string; reward_balance?: string;
}
export interface BlockSummary {
  height: number; hash: string; time: string; proposer: string; tx_count: number; exec_hash: string;
}
export interface BlocksResponse { blocks: BlockSummary[]; total: number; }
export interface BlockResponse {
  height: number; hash: string; time: string; proposer: string;
  txs: string[]; exec_hash: string; gas_used: string; gas_limit: string;
}
export interface TxResponse {
  hash: string; block_height: number; status: string; from: string; to: string;
  value: string; gas_used: string; gas_price: string; nonce: number;
}
export interface AddressResponse { address: string; balance: string; nonce: number; code: string; }
export interface Peer { id: string; remote_ip: string; is_outbound: boolean; moniker?: string; }
export interface PeersResponse { peers: Peer[]; }
export interface FaucetResponse { tx_hash: string; amount: string; message?: string; }

export interface StakingValidator {
  evm_addr: string; comet_addr: string; ed25519_pubkey: string; reward_addr: string;
  stake: string; whitelisted: boolean; active: boolean; jailed: boolean;
  unbonding_unlock_block: number; voting_power: number;
  moniker?: string; website?: string;
}
export interface StakingResponse {
  enabled: boolean; contract_addr?: string; owner?: string;
  min_stake?: string; unbonding_period?: number; validators: StakingValidator[];
}

export interface NodeWalletResponse {
  address: string; balance_wei: string; has_key: boolean;
}

export interface StakingTxResponse { tx_hash: string; moniker_tx?: string; website_tx?: string; }

export const CHAIN_ID = 11640;

// When served from localhost (a self-hosted chakram-node), point directly at
// the local geth RPC — MetaMask and staking calls never leave the machine.
// When served from node.chakram.one use the public endpoints.
function _isLocal(): boolean {
  if (typeof window === 'undefined') return false;
  const h = window.location.hostname;
  return h === 'localhost' || h === '127.0.0.1' ||
    h.startsWith('192.168.') || h.startsWith('10.') || h.startsWith('172.');
}

export const IS_LOCAL     = _isLocal();
export const GETH_RPC     = IS_LOCAL ? 'http://localhost:8545'  : 'https://rpc.chakram.one';
export const EXPLORER_URL = IS_LOCAL
  ? (typeof window !== 'undefined' ? window.location.origin : 'http://localhost:8080')
  : 'https://node.chakram.one';

export function getGethRpc(): string { return GETH_RPC; }
export function getBase(): string { return ''; }

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export const api = {
  status: () => get<StatusResponse>('/api/status'),
  validator: () => get<ValidatorResponse>('/api/validator'),
  blocks: (limit = 20, offset = 0) => get<BlocksResponse>(`/api/blocks?limit=${limit}&offset=${offset}`),
  block: (height: number) => get<BlockResponse>(`/api/block/${height}`),
  tx: (hash: string) => get<TxResponse>(`/api/tx/${hash}`),
  address: (addr: string) => get<AddressResponse>(`/api/address/${addr}`),
  peers: () => get<PeersResponse>('/api/peers'),
  staking: () => get<StakingResponse>('/api/staking'),
  faucet: (address: string) =>
    fetch(getBase() + '/api/faucet', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address }),
    }).then(r => r.json() as Promise<FaucetResponse | { error: string }>),
  logsUrl: () => getBase() + '/api/logs',
  nodeWallet: () => get<NodeWalletResponse>('/api/node-wallet'),
  stakingRegister: (rewardAddress?: string, stakeWei?: string, moniker?: string, website?: string) =>
    fetch('/api/staking/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        reward_address: rewardAddress || undefined,
        stake_wei: stakeWei || undefined,
        moniker: moniker || undefined,
        website: website || undefined,
      }),
    }).then(r => r.json() as Promise<StakingTxResponse | { error: string }>),
  stakingUnbond: () =>
    fetch('/api/staking/unbond', { method: 'POST' }).then(r => r.json() as Promise<StakingTxResponse | { error: string }>),
  stakingWithdraw: () =>
    fetch('/api/staking/withdraw', { method: 'POST' }).then(r => r.json() as Promise<StakingTxResponse | { error: string }>),
  stakingWhitelistStatus: () => get<{ whitelisted: boolean; address: string }>('/api/staking/whitelist-status'),
};

export function trunc(s: string, n = 12): string {
  if (!s) return '';
  if (s.length <= n * 2 + 3) return s;
  return s.slice(0, n) + '…' + s.slice(-6);
}

export function formatTime(iso: string): string {
  try { return new Date(iso).toLocaleString(); } catch { return iso; }
}
