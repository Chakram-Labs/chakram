import { useEffect, useState } from 'react';
import { Link, useParams, useLocation, useNavigate } from 'react-router-dom';
import { api, BlockSummary, BlockResponse, trunc, formatTime } from '../api/client';
import { ChevronLeft, ChevronRight, ArrowLeft } from 'lucide-react';
import Tx from './Tx';
import { GradCard, Chip } from '../components/ui';

function BlockDetail({ height }: { height: number }) {
  const [data, setData] = useState<BlockResponse | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    api.block(height).then(d => { setData(d); setErr(''); }).catch(e => setErr(String(e)));
  }, [height]);

  if (err) return <p className="text-sm text-red-600 dark:text-red-400">{err}</p>;
  if (!data) return (
    <div className="flex items-center gap-2 text-sm text-slate-500 dark:text-gray-400">
      <span className="size-1.5 rounded-full bg-indigo-500 animate-pulse" />
      Loading…
    </div>
  );

  const fields: [string, string][] = [
    ['Height',    String(data.height)],
    ['Hash',      data.hash],
    ['Time',      formatTime(data.time)],
    ['Proposer',  data.proposer],
    ['Exec Hash', data.exec_hash],
    ['Gas Used',  data.gas_used || '—'],
    ['Gas Limit', data.gas_limit || '—'],
  ];

  return (
    <div className="space-y-6">
      <Link
        to="/blocks"
        className="inline-flex items-center gap-1.5 text-sm text-slate-500 dark:text-gray-400 hover:text-slate-800 dark:hover:text-white transition-colors"
      >
        <ArrowLeft size={14} strokeWidth={2} />
        Back to Blocks
      </Link>

      {/* Block details table card */}
      <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
        <div className="bg-gradient-to-tr from-gray-900 to-gray-800 px-6 py-5">
          <h6 className="text-sm font-semibold text-white">Block #{data.height}</h6>
          <p className="mt-0.5 text-[11px] text-white/50">{formatTime(data.time)}</p>
        </div>
        <div className="bg-white dark:bg-gray-800 overflow-x-auto px-0 pt-0 pb-2">
          <table className="w-full table-auto">
            <tbody>
              {fields.map(([k, v], i, arr) => (
                <tr key={k}>
                  <td className={`py-3 px-5 w-36 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{k}</span>
                  </td>
                  <td className={`py-3 px-5 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className="font-mono text-xs text-slate-700 dark:text-white break-all">{v}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Transactions in this block */}
      {data.txs.length > 0 && (
        <GradCard
          title="Transactions"
          subtitle={`${data.txs.length} transaction${data.txs.length !== 1 ? 's' : ''} in this block`}
        >
          <table className="w-full table-auto">
            <thead>
              <tr>
                <th className="border-b border-slate-50 dark:border-gray-700 py-3 px-5 text-left">
                  <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">Tx Hash</span>
                </th>
                <th className="border-b border-slate-50 dark:border-gray-700 py-3 px-5 text-left">
                  <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">Action</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {data.txs.map((tx, i) => (
                <tr key={tx} className="hover:bg-slate-50/80 dark:hover:bg-gray-700/30 transition-colors">
                  <td className={`py-3 px-5 ${i < data.txs.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className="font-mono text-xs text-slate-600 dark:text-gray-300">{trunc(tx, 20)}</span>
                  </td>
                  <td className={`py-3 px-5 ${i < data.txs.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <Link
                      to={`/tx/${tx}`}
                      className="text-xs font-medium text-indigo-600 dark:text-indigo-400 hover:underline"
                    >
                      View →
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </GradCard>
      )}
    </div>
  );
}

export default function Blocks() {
  const { height, hash } = useParams();
  const location = useLocation();
  if (location.pathname.startsWith('/tx/') && hash) return <Tx />;
  if (height) return <BlockDetail height={parseInt(height)} />;
  return <BlockList />;
}

function BlockList() {
  const [blocks, setBlocks] = useState<BlockSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [err, setErr] = useState('');
  const navigate = useNavigate();
  const PAGE = 20;

  async function load(p: number) {
    try {
      const r = await api.blocks(PAGE, p * PAGE);
      setBlocks(r.blocks); setTotal(r.total); setErr('');
    } catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => {
    load(page);
    const id = setInterval(() => load(page), 5000);
    return () => clearInterval(id);
  }, [page]);

  const pages = Math.ceil(total / PAGE);

  if (err) return <p className="text-sm text-red-600 dark:text-red-400">{err}</p>;

  return (
    <div className="space-y-6">
      <GradCard
        title="Block Explorer"
        subtitle={`${total.toLocaleString()} total blocks`}
      >
        <table className="w-full min-w-[640px] table-auto">
          <thead>
            <tr>
              {(['Height', 'Hash', 'Time', 'Txs', 'Exec Hash'] as const).map(h => (
                <th key={h} className="border-b border-slate-50 dark:border-gray-700 py-3 px-5 text-left">
                  <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{h}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {blocks.map((b, i) => (
              <tr
                key={b.height}
                className="cursor-pointer hover:bg-slate-50/80 dark:hover:bg-gray-700/30 transition-colors"
                onClick={() => navigate(`/block/${b.height}`)}
              >
                <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                  <span className="text-sm font-semibold tabular-nums text-indigo-600 dark:text-indigo-400">
                    #{b.height}
                  </span>
                </td>
                <td className={`py-3 px-5 max-w-[160px] ${i < blocks.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                  <span className="font-mono text-xs text-slate-500 dark:text-gray-400 truncate block">{trunc(b.hash)}</span>
                </td>
                <td className={`py-3 px-5 whitespace-nowrap ${i < blocks.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                  <span className="text-xs text-slate-500 dark:text-gray-400">{formatTime(b.time)}</span>
                </td>
                <td className={`py-3 px-5 ${i < blocks.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                  {b.tx_count > 0
                    ? <Chip color="lime">{b.tx_count} tx</Chip>
                    : <span className="text-xs tabular-nums text-slate-400 dark:text-gray-500">0</span>
                  }
                </td>
                <td className={`py-3 px-5 max-w-[160px] ${i < blocks.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                  <span className="font-mono text-xs text-slate-500 dark:text-gray-400 truncate block">{trunc(b.exec_hash)}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </GradCard>

      {/* Pagination */}
      {pages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <button
            disabled={page === 0}
            onClick={() => setPage(p => p - 1)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-slate-700 dark:text-white hover:bg-slate-50 dark:hover:bg-gray-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            <ChevronLeft size={14} /> Prev
          </button>
          <span className="px-3 text-sm text-slate-500 dark:text-gray-400 tabular-nums">
            {page + 1} / {pages}
          </span>
          <button
            disabled={page >= pages - 1}
            onClick={() => setPage(p => p + 1)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-slate-700 dark:text-white hover:bg-slate-50 dark:hover:bg-gray-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            Next <ChevronRight size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
