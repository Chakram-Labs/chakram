import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, StatusResponse, BlockSummary, trunc, formatTime, getGethRpc, getBase } from '../api/client';
import { AlertTriangle, AlertCircle, Layers3, Cpu, Network, Link2, RefreshCw, Wallet } from 'lucide-react';
import { StatCard, GradCard, Chip } from '../components/ui';

// ─── MetaMask add-network card ────────────────────────────────────────────────

declare global {
  interface Window {
    ethereum?: { request: (a: { method: string; params?: unknown[] }) => Promise<unknown>; isMetaMask?: boolean };
  }
}

function MetaMaskCard({ chainId }: { chainId: string }) {
  const [state, setState] = useState<'idle' | 'ok' | 'err'>('idle');
  const [msg, setMsg] = useState('');

  const hasMetaMask = typeof window !== 'undefined' && !!window.ethereum;
  // EVM chain ID from CometBFT chain_id is not the same thing —
  // we use the fixed network ID 11642 (0x2D7A) set in .env.
  const evmChainIdHex = '0x2D7A';
  const rpcUrl       = getGethRpc();
  const explorerUrl  = getBase();

  async function addNetwork() {
    if (!window.ethereum) return;
    setState('idle'); setMsg('');
    try {
      await window.ethereum.request({
        method: 'wallet_addEthereumChain',
        params: [{
          chainId: evmChainIdHex,
          chainName: 'Chakram',
          nativeCurrency: { name: 'Chakram', symbol: 'CHK', decimals: 18 },
          rpcUrls: [rpcUrl],
          blockExplorerUrls: [explorerUrl],
        }],
      });
      setState('ok'); setMsg('Network added to MetaMask!');
    } catch (err: unknown) {
      setState('err');
      setMsg(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="rounded-xl border border-slate-100 border-white/5 bg-white bg-gray-900 shadow-sm overflow-hidden">
      <div className="bg-gradient-to-tr from-orange-500 to-amber-400 px-6 py-5 flex items-center gap-3">
        <Wallet size={16} className="text-white" strokeWidth={2} />
        <div>
          <h6 className="text-sm font-semibold text-white">Connect Wallet</h6>
          <p className="mt-0.5 text-[11px] text-white/70">Add Chakram to MetaMask or any EVM wallet</p>
        </div>
      </div>
      <div className="px-6 py-5">
        <div className="mb-4 grid grid-cols-2 gap-x-6 gap-y-2 text-xs">
          {[
            ['Network',   'Chakram'],
            ['Symbol',    'CHK'],
            ['Chain ID',  '11642'],
            ['Decimals',  '18'],
            ['RPC URL',   rpcUrl],
            ['Explorer',  explorerUrl],
          ].map(([k, v]) => (
            <div key={k} className="flex flex-col">
              <span className="text-[10px] font-bold uppercase text-gray-500">{k}</span>
              <span className="font-mono text-white truncate">{v}</span>
            </div>
          ))}
        </div>

        {hasMetaMask ? (
          <button
            onClick={addNetwork}
            className="inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-orange-500 to-amber-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-orange-500/20 hover:shadow-orange-500/40 transition-shadow"
          >
            <Wallet size={14} strokeWidth={2} />
            Add Chakram to MetaMask
          </button>
        ) : (
          <p className="text-xs text-gray-500">
            MetaMask not detected — install it, then add the network details above manually,
            or use any EVM-compatible wallet (Rabby, Frame, Coinbase Wallet).
          </p>
        )}

        {state === 'ok' && <p className="mt-2 text-xs text-emerald-600 dark:text-emerald-400">{msg}</p>}
        {state === 'err' && <p className="mt-2 text-xs text-red-500 dark:text-red-400">{msg}</p>}
      </div>
    </div>
  );
}

function Banner({ variant, text }: { variant: 'error' | 'warning'; text: string }) {
  const s = variant === 'error'
    ? 'bg-red-50 dark:bg-red-500/10 border-red-200 dark:border-red-500/20 text-red-700 dark:text-red-400'
    : 'bg-amber-50 dark:bg-amber-500/10 border-amber-200 dark:border-amber-500/20 text-amber-700 dark:text-amber-400';
  const Icon = variant === 'error' ? AlertCircle : AlertTriangle;
  return (
    <div className={`flex items-start gap-3 rounded-xl border px-4 py-3 text-sm font-medium ${s}`}>
      <Icon size={15} className="mt-0.5 shrink-0" strokeWidth={2} />
      {text}
    </div>
  );
}

export default function Overview() {
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [blocks, setBlocks] = useState<BlockSummary[]>([]);
  const [err, setErr] = useState('');
  const prevHeightRef = useRef<number>(0);
  const lastHeightChangeRef = useRef<number>(Date.now());
  const [halted, setHalted] = useState(false);
  const navigate = useNavigate();

  async function load() {
    try {
      const [s, b] = await Promise.all([api.status(), api.blocks(6, 0)]);
      setStatus(s); setBlocks(b.blocks); setErr('');
      if (s.height !== prevHeightRef.current) {
        prevHeightRef.current = s.height;
        lastHeightChangeRef.current = Date.now();
        setHalted(false);
      } else if (Date.now() - lastHeightChangeRef.current > 30_000) {
        setHalted(true);
      }
    } catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, []);

  if (err && !status) return (
    <div className="mt-12 space-y-3">
      <Banner variant="error" text="CometBFT unreachable — node API cannot connect to consensus layer" />
      <p className="text-xs font-mono text-gray-500 pl-1">{err}</p>
    </div>
  );

  if (!status) return (
    <div className="mt-12 flex items-center gap-2 text-sm text-gray-500">
      <span className="size-1.5 rounded-full bg-indigo-500 animate-pulse" />
      Connecting…
    </div>
  );

  return (
    // mt-12 gives the first row of floating stat-card icons room above the card top
    <div className="mt-12">

      {/* Stat cards — 4-col grid */}
      <div className="mb-12 grid gap-y-10 gap-x-6 md:grid-cols-2 xl:grid-cols-4">

        <StatCard
          color="gray"
          icon={Layers3}
          title="CometBFT Height"
          value={status.height.toLocaleString()}
          footer={
            <Chip color={status.catching_up ? 'amber' : 'lime'}>
              {status.catching_up ? 'Catching up' : 'Synced'}
            </Chip>
          }
        />

        <StatCard
          color="lime"
          icon={Cpu}
          title="Geth Head"
          value={status.geth_available ? status.geth_height.toLocaleString() : '—'}
          footer={
            status.geth_available
              ? <span className="text-[11px] text-gray-500">finalized: {status.geth_finalized}</span>
              : <Chip color="red">Unreachable</Chip>
          }
        />

        <StatCard
          color="teal"
          icon={Network}
          title="Peers"
          value={status.peers}
          footer={<span className="text-[11px] text-gray-500">connected peers</span>}
        />

        <StatCard
          color="lime"
          icon={Link2}
          title="Chain ID"
          value={status.chain_id}
          footer={
            status.health_gate_blocked
              ? <Chip color="amber">Gate blocked</Chip>
              : <Chip color="lime">Healthy</Chip>
          }
        />
      </div>

      {/* Banners */}
      {(!status.geth_available || err || (halted && !status.catching_up) || status.health_gate_blocked) && (
        <div className="mb-8 space-y-2">
          {!status.geth_available && (
            <Banner variant="error" text="Geth execution layer is unreachable — blocks cannot be produced" />
          )}
          {err && (
            <Banner variant="error" text="CometBFT unreachable — consensus data may be stale" />
          )}
          {halted && status.geth_available && !status.catching_up && (
            <Banner variant="warning" text="Chain may be halted — height has not changed in 30 seconds" />
          )}
          {status.health_gate_blocked && (
            <Banner variant="warning" text={`Health gate blocking proposals — geth (${status.geth_height}) is >2 blocks behind CometBFT (${status.height})`} />
          )}
        </div>
      )}

      {/* Latest blocks + Node info */}
      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-3">

        {/* Latest blocks table */}
        <div className="xl:col-span-2">
          <GradCard
            title="Latest Blocks"
            subtitle="Most recent blocks on the chain"
            action={
              <Link
                to="/blocks"
                className="text-[11px] font-medium text-white/70 hover:text-white transition-colors"
              >
                View all →
              </Link>
            }
          >
            <table className="w-full min-w-[640px] table-auto">
              <thead>
                <tr>
                  {['Height', 'Hash', 'Time', 'Txs'].map(h => (
                    <th key={h} className="border-b border-white/5 py-3 px-5 text-left">
                      <span className="text-[11px] font-bold uppercase text-gray-500">{h}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {blocks.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="py-10 text-center text-sm text-gray-500">
                      No blocks yet
                    </td>
                  </tr>
                ) : blocks.map((b, i) => (
                  <tr
                    key={b.height}
                    className="cursor-pointer hover:bg-slate-50/80 dark:hover:bg-gray-700/30 transition-colors"
                    onClick={() => navigate(`/block/${b.height}`)}
                  >
                    <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="text-sm font-semibold tabular-nums text-lime-400">
                        #{b.height}
                      </span>
                    </td>
                    <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="font-mono text-xs text-gray-500">
                        {trunc(b.hash)}
                      </span>
                    </td>
                    <td className={`py-3 px-5 whitespace-nowrap ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="text-xs text-gray-500">{formatTime(b.time)}</span>
                    </td>
                    <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="text-xs tabular-nums text-gray-500">{b.tx_count}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </GradCard>
        </div>

        {/* Node Info card */}
        <div className="rounded-xl border border-white/5 bg-gray-900 shadow-sm overflow-hidden">
          <div className="bg-gray-800 px-6 py-5 border-b border-white/5">
            <h6 className="text-sm font-semibold text-white">Node Info</h6>
            <p className="mt-0.5 text-[11px] text-white/50">CometBFT node details</p>
          </div>
          <dl className="px-0">
            {([
              ['Node ID',    trunc(status.node_id, 10),         true],
              ['Moniker',    status.moniker,                     false],
              ['Version',    `v${status.comet_version}`,         false],
              ['App Hash',   trunc(status.latest_app_hash, 10),  true],
            ] as [string, string, boolean][]).map(([label, value, mono], i, arr) => (
              <div key={label} className={`flex items-start justify-between px-6 py-3 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                <dt className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 shrink-0 mr-4">
                  {label}
                </dt>
                <dd className={`text-xs text-right text-white break-all ${mono ? 'font-mono' : 'font-medium'}`}>
                  {value}
                </dd>
              </div>
            ))}
            {/* Live indicator */}
            <div className="flex items-center justify-between px-6 py-3 border-t border-white/5">
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-gray-500">Status</dt>
              <dd className="flex items-center gap-1.5">
                <span className="relative flex size-2">
                  {!err && !halted && (
                    <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-lime-400 opacity-75" />
                  )}
                  <span className={`relative inline-flex size-2 rounded-full ${err || halted ? 'bg-red-500' : 'bg-lime-400'}`} />
                </span>
                <span className="text-xs font-medium text-gray-300">
                  {err ? 'Disconnected' : halted ? 'Halted' : 'Live'}
                </span>
              </dd>
            </div>
          </dl>
        </div>
      </div>

      {/* MetaMask connect */}
      <div className="mb-6">
        <MetaMaskCard chainId={status.chain_id} />
      </div>

      {/* Height gap info */}
      {status.geth_available && Math.abs(status.height - status.geth_height) > 0 && (
        <div className="rounded-xl border border-slate-100 border-white/5 bg-white bg-gray-900 shadow-sm px-5 py-4 flex items-center gap-3">
          <RefreshCw size={14} className="text-gray-500 shrink-0" strokeWidth={2} />
          <span className="text-sm text-gray-500">Height gap: </span>
          <span className={`text-sm font-semibold ${Math.abs(status.height - status.geth_height) > 2 ? 'text-amber-600 dark:text-amber-400' : 'text-white'}`}>
            CometBFT {status.height} · Geth {status.geth_height} · Finalized {status.geth_finalized}
          </span>
        </div>
      )}
    </div>
  );
}
