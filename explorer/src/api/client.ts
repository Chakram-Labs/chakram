const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8081/api/v1'

async function get<T>(path: string): Promise<T> {
  const r = await fetch(`${BASE}${path}`)
  if (!r.ok) throw new Error(`${r.status}`)
  return r.json()
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) {
    const err = await r.json().catch(() => ({ error: r.statusText }))
    throw new Error(err.error ?? r.statusText)
  }
  return r.json()
}

export interface Stats {
  latest_block: number
  total_txs: number
  avg_block_time_s: number
  total_tokens: number
  total_contracts: number
  total_verified_contracts: number
  indexed_through: number
  indexer_status: string
}

export interface Block {
  height: number
  hash: string
  parent_hash: string
  comet_hash: string
  proposer: string
  time: number
  tx_count: number
  gas_used: number
  gas_limit: number
  size: number
  app_hash: string
  base_fee: string
}

export interface Tx {
  hash: string
  block_height: number
  block_hash: string
  tx_index: number
  from: string
  to: string
  value: string
  gas_limit: number
  gas_used: number
  gas_price: string
  max_fee_per_gas: string
  max_priority_fee_per_gas: string
  nonce: number
  input: string
  status: number
  contract_address: string
  type: number
}

export interface Log {
  id: number
  tx_hash: string
  block_height: number
  log_index: number
  address: string
  topic0: string
  topic1: string
  topic2: string
  topic3: string
  data: string
}

export interface TokenTransfer {
  id: number
  tx_hash: string
  block_height: number
  log_index: number
  token_address: string
  from: string
  to: string
  value: string
  token_id: string
  token_type: string
}

export interface Token {
  address: string
  name: string
  symbol: string
  decimals: number
  type: string
  total_supply: string
  holder_count: number
  transfer_count: number
  first_seen_block: number
}

export interface TokenHolder {
  address: string
  balance: string
}

export interface AddressToken {
  token_address: string
  name: string
  symbol: string
  decimals: number
  type: string
  balance: string
}

export interface Contract {
  address: string
  creator: string
  creation_tx: string
  creation_block: number
  bytecode?: string
  is_verified: boolean
  name: string
  source_json?: string
  abi?: string
  compiler_version: string
  optimization: boolean
  optimization_runs: number
  constructor_args: string
  verified_at: number
  license: string
}

export interface Validator {
  address: string
  voting_power: string
  pub_key: { type: string; value: string }
  evm_addr?: string
  reward_addr?: string
  stake?: string
  moniker?: string
  website?: string
  active?: boolean
  jailed?: boolean
}

export interface SearchResult {
  type: 'block' | 'tx' | 'address' | 'contract' | 'token' | 'not_found'
  value: string | number
}

export interface IndexerStatus {
  last_indexed_block: number
  chain_head: number
  blocks_behind: number
  is_synced: boolean
}

// API calls

export const api = {
  stats: () => get<Stats>('/stats'),
  indexerStatus: () => get<IndexerStatus>('/indexer/status'),
  search: (q: string) => get<SearchResult>(`/search?q=${encodeURIComponent(q)}`),

  blocks: (limit = 25, page = 1) => get<{ blocks: Block[]; total: number; limit: number }>(`/blocks?limit=${limit}&page=${page}`),
  latestBlock: () => get<Block>('/blocks/latest'),
  block: (height: string | number) => get<Block>(`/blocks/${height}`),
  blockTxs: (height: string | number) => get<{ txs: Tx[]; total: number }>(`/blocks/${height}/txs`),

  txs: (limit = 25, page = 1) => get<{ txs: Tx[]; total: number; limit: number }>(`/txs?limit=${limit}&page=${page}`),
  tx: (hash: string) => get<{ tx: Tx; logs: Log[] }>(`/txs/${hash}`),
  txLogs: (hash: string) => get<{ logs: Log[] }>(`/txs/${hash}/logs`),

  address: (addr: string) => get<{
    address: string; balance: string; tx_count: number; is_contract: boolean;
    contract?: { is_verified: boolean; name: string; compiler_version: string; creator: string; creation_tx: string; creation_block: number };
    token?: Token
  }>(`/addresses/${addr}`),
  addressTxs: (addr: string, limit = 25, page = 1) => get<{ txs: Tx[]; total: number }>(`/addresses/${addr}/txs?limit=${limit}&page=${page}`),
  addressTokenTransfers: (addr: string, limit = 25, page = 1) => get<{ transfers: TokenTransfer[]; total: number }>(`/addresses/${addr}/token-transfers?limit=${limit}&page=${page}`),
  addressTokens: (addr: string) => get<{ tokens: AddressToken[] }>(`/addresses/${addr}/tokens`),

  tokens: (limit = 25, page = 1) => get<{ tokens: Token[]; total: number }>(`/tokens?limit=${limit}&page=${page}`),
  token: (addr: string) => get<Token>(`/tokens/${addr}`),
  tokenTransfers: (addr: string, limit = 25, page = 1) => get<{ transfers: TokenTransfer[]; total: number }>(`/tokens/${addr}/transfers?limit=${limit}&page=${page}`),
  tokenHolders: (addr: string, limit = 25, page = 1) => get<{ holders: TokenHolder[]; total: number }>(`/tokens/${addr}/holders?limit=${limit}&page=${page}`),

  contract: (addr: string) => get<Contract>(`/contracts/${addr}`),
  contractABI: (addr: string) => get<unknown>(`/contracts/${addr}/abi`),
  verifyContract: (addr: string, body: unknown) => post<{ success: boolean; message: string; contract_name?: string; abi?: string }>(`/contracts/${addr}/verify`, body),

  validators: () => get<{ height: number; validators: Validator[] }>('/validators'),
}

// Utilities

export function trunc(s: string, chars = 8): string {
  if (!s || s.length <= chars * 2 + 2) return s
  return `${s.slice(0, chars)}…${s.slice(-4)}`
}

export function formatTime(unix: number, nowUnix?: number): string {
  if (!unix) return '—'
  const ref = nowUnix ?? Math.floor(Date.now() / 1000)
  const diff = Math.max(0, ref - unix)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return new Date(unix * 1000).toLocaleDateString()
}

export function formatWei(wei: string, decimals = 18): string {
  if (!wei || wei === '0') return '0'
  try {
    const n = BigInt(wei)
    const divisor = BigInt(10 ** decimals)
    const whole = n / divisor
    const frac = n % divisor
    if (frac === 0n) return whole.toString()
    const fracStr = frac.toString().padStart(decimals, '0').replace(/0+$/, '').slice(0, 6)
    return `${whole}.${fracStr}`
  } catch { return wei }
}

export function formatNumber(n: number | string): string {
  return Number(n).toLocaleString()
}
