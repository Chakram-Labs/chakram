import { ethers } from 'ethers';
import { GETH_RPC, CHAIN_ID, EXPLORER_URL } from '../api/client';

declare global {
  interface Window {
    ethereum?: { request: (a: { method: string; params?: unknown[] }) => Promise<unknown> };
  }
}

type EthProvider = {
  request: (args: { method: string; params?: unknown[] }) => Promise<unknown>;
  on: (event: string, cb: (...args: unknown[]) => void) => void;
  removeListener: (event: string, cb: (...args: unknown[]) => void) => void;
};

export function getEthProvider(): EthProvider | undefined {
  return window.ethereum as unknown as EthProvider | undefined;
}

export const CHAKRAM_CHAIN = {
  chainId: '0x' + CHAIN_ID.toString(16),
  chainName: 'Chakram',
  nativeCurrency: { name: 'Chakram', symbol: 'CHK', decimals: 18 },
  rpcUrls: [GETH_RPC],
  blockExplorerUrls: [EXPLORER_URL],
};

export const STAKING_ABI = [
  'function whitelist(address validator)',
  'function removeWhitelist(address validator)',
  'function jail(address validator)',
  'function unjail(address validator)',
  'function register(bytes32 ed25519PubKey, address rewardAddress) payable',
  'function startUnbonding()',
  'function withdraw()',
  'function owner() view returns (address)',
  'function minStake() view returns (uint256)',
  'function isActive(address) view returns (bool)',
  'function validators(address) view returns (bytes32, address, uint256, bool, bool, bool, bool, uint16, uint256)',
];

export function getReadProvider(): ethers.JsonRpcProvider {
  return new ethers.JsonRpcProvider(GETH_RPC);
}

export function getReadContract(contractAddr: string): ethers.Contract {
  return new ethers.Contract(contractAddr, STAKING_ABI, getReadProvider());
}

export async function ensureChakramNetwork(): Promise<void> {
  const eth = getEthProvider();
  if (!eth) throw new Error('No wallet detected. Install MetaMask.');
  const chainId = await eth.request({ method: 'eth_chainId' }) as string;
  if (parseInt(chainId, 16) === CHAIN_ID) return;
  try {
    await eth.request({ method: 'wallet_switchEthereumChain', params: [{ chainId: CHAKRAM_CHAIN.chainId }] });
  } catch (err) {
    if ((err as { code?: number }).code === 4902) {
      await eth.request({ method: 'wallet_addEthereumChain', params: [CHAKRAM_CHAIN] });
    } else {
      throw err;
    }
  }
}

export async function connectWallet(): Promise<string> {
  const eth = getEthProvider();
  if (!eth) throw new Error('No wallet detected. Install MetaMask.');
  const accounts = await eth.request({ method: 'eth_requestAccounts' }) as string[];
  await ensureChakramNetwork();
  return accounts[0];
}

export async function getSignerContract(contractAddr: string): Promise<ethers.Contract> {
  if (!window.ethereum) throw new Error('No wallet detected. Install MetaMask.');
  await ensureChakramNetwork();
  const provider = new ethers.BrowserProvider(window.ethereum as ethers.Eip1193Provider);
  const signer = await provider.getSigner();
  return new ethers.Contract(contractAddr, STAKING_ABI, signer);
}

export function base64ToPubKeyHex(b64: string): string {
  try {
    const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
    if (bytes.length !== 32) return '';
    return '0x' + Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
  } catch {
    return '';
  }
}

export function weiToChkDisplay(wei: string): string {
  try {
    const chk = Number(BigInt(wei)) / 1e18;
    return chk.toLocaleString(undefined, { maximumFractionDigits: 4 }) + ' CHK';
  } catch { return wei + ' wei'; }
}
