import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, StatusResponse, BlockSummary, trunc, formatTime } from '../api/client';
import { AlertTriangle, AlertCircle, Layers3, Cpu, Network, Link2, RefreshCw } from 'lucide-react';
import { StatCard, GradCard, Chip } from '../components/ui';

function Banner({ variant, text }: { variant: 'error' | 'warning'; text: string }) {
  const s = variant === 'error'
    ? 'bg-red-500/8 border-red-500/20 text-red-400'
    : 'bg-amber-500/8 border-amber-500/20 text-amber-400';
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
    <div className="mt-6 space-y-3">
      <Banner variant="error" text="CometBFT unreachable — node API cannot connect to consensus layer" />
      <p className="text-xs font-mono text-gray-600 pl-1">{err}</p>
    </div>
  );

  if (!status) return (
    <div className="mt-6 flex items-center gap-2 text-sm text-gray-500">
      <span className="size-1.5 rounded-full bg-lime-400 animate-pulse" />
      Connecting…
    </div>
  );

  return (
    <div className="mt-6">

      {/* Stat cards */}
      <div className="mb-8 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
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
          color="lime"
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

        <div className="xl:col-span-2">
          <GradCard
            title="Latest Blocks"
            subtitle="Most recent blocks on the chain"
            action={
              <Link to="/blocks" className="text-[11px] font-medium text-gray-500 hover:text-white transition-colors">
                View all →
              </Link>
            }
          >
            <table className="w-full min-w-[640px] table-auto">
              <thead>
                <tr>
                  {['Height', 'Hash', 'Time', 'Txs'].map(h => (
                    <th key={h} className="border-b border-white/5 py-3 px-5 text-left">
                      <span className="text-[11px] font-bold uppercase text-gray-600">{h}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {blocks.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="py-10 text-center text-sm text-gray-600">
                      No blocks yet
                    </td>
                  </tr>
                ) : blocks.map((b, i) => (
                  <tr
                    key={b.height}
                    className="cursor-pointer hover:bg-white/4 transition-colors"
                    onClick={() => navigate(`/block/${b.height}`)}
                  >
                    <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="text-sm font-semibold tabular-nums text-lime-400">#{b.height}</span>
                    </td>
                    <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-white/5' : ''}`}>
                      <span className="font-mono text-xs text-gray-500">{trunc(b.hash)}</span>
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

        {/* Node Info */}
        <div className="rounded-xl border border-white/8 bg-[#111] overflow-hidden">
          <div className="bg-[#161616] px-6 py-4 border-b border-white/8">
            <h6 className="text-sm font-semibold text-white">Node Info</h6>
            <p className="mt-0.5 text-[11px] text-gray-500">CometBFT node details</p>
          </div>
          <dl className="px-0">
            {([
              ['Node ID',  trunc(status.node_id, 10),        true],
              ['Moniker',  status.moniker,                    false],
              ['Version',  `v${status.comet_version}`,        false],
              ['App Hash', trunc(status.latest_app_hash, 10), true],
            ] as [string, string, boolean][]).map(([label, value, mono], i, arr) => (
              <div key={label} className={`flex items-start justify-between px-6 py-3 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                <dt className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 shrink-0 mr-4">{label}</dt>
                <dd className={`text-xs text-right text-white break-all ${mono ? 'font-mono' : 'font-medium'}`}>{value}</dd>
              </div>
            ))}
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

      {/* Height gap */}
      {status.geth_available && Math.abs(status.height - status.geth_height) > 0 && (
        <div className="rounded-xl border border-white/8 bg-[#111] px-5 py-4 flex items-center gap-3">
          <RefreshCw size={14} className="text-gray-600 shrink-0" strokeWidth={2} />
          <span className="text-sm text-gray-500">Height gap: </span>
          <span className={`text-sm font-semibold ${Math.abs(status.height - status.geth_height) > 2 ? 'text-amber-400' : 'text-white'}`}>
            CometBFT {status.height} · Geth {status.geth_height} · Finalized {status.geth_finalized}
          </span>
        </div>
      )}
    </div>
  );
}
