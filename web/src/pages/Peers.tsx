import { useEffect, useState } from 'react';
import { api, Peer } from '../api/client';
import { ArrowUpRight, ArrowDownLeft } from 'lucide-react';
import { GradCard, Chip } from '../components/ui';

export default function Peers() {
  const [peers, setPeers] = useState<Peer[]>([]);
  const [err, setErr] = useState('');

  async function load() {
    try { setPeers((await api.peers()).peers); setErr(''); }
    catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, []);

  if (err) return <p className="text-sm text-red-600 dark:text-red-400">{err}</p>;

  if (peers.length === 0) return (
    <GradCard title="Peers" subtitle="No peers connected yet">
      <div className="py-16 text-center">
        <p className="text-sm text-slate-400 dark:text-gray-500">No peers connected</p>
        <p className="mt-1 text-xs text-slate-300 dark:text-gray-600">Waiting for peer connections…</p>
      </div>
    </GradCard>
  );

  const inbound = peers.filter(p => !p.is_outbound).length;
  const outbound = peers.filter(p => p.is_outbound).length;

  return (
    <GradCard
      title="Connected Peers"
      subtitle={`${peers.length} total · ${outbound} outbound · ${inbound} inbound`}
    >
      <table className="w-full min-w-[640px] table-auto">
        <thead>
          <tr>
            {(['Node ID', 'Remote IP', 'Moniker', 'Direction'] as const).map(h => (
              <th key={h} className="border-b border-slate-50 dark:border-gray-700 py-3 px-5 text-left">
                <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{h}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {peers.map((p, i) => (
            <tr key={p.id} className="hover:bg-slate-50/80 dark:hover:bg-gray-700/30 transition-colors">
              <td className={`py-3 px-5 max-w-[200px] ${i < peers.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                <span className="font-mono text-xs text-slate-500 dark:text-gray-400 truncate block">
                  {p.id.slice(0, 20)}…
                </span>
              </td>
              <td className={`py-3 px-5 ${i < peers.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                <span className="font-mono text-xs text-slate-700 dark:text-white">{p.remote_ip}</span>
              </td>
              <td className={`py-3 px-5 ${i < peers.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                <span className="text-sm text-slate-600 dark:text-gray-300">{p.moniker || '—'}</span>
              </td>
              <td className={`py-3 px-5 ${i < peers.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                <Chip color={p.is_outbound ? 'amber' : 'green'}>
                  {p.is_outbound
                    ? <><ArrowUpRight size={9} strokeWidth={2.5} /> out</>
                    : <><ArrowDownLeft size={9} strokeWidth={2.5} /> in</>
                  }
                </Chip>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </GradCard>
  );
}
