import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, TxResponse } from '../api/client';
import { ArrowLeft, CheckCircle2, XCircle, Clock } from 'lucide-react';
import { Card, Chip } from '../components/ui';

function weiToChk(wei: string): string {
  try {
    const chk = Number(BigInt(wei)) / 1e18;
    return chk === 0 ? '0 CHK' : chk.toFixed(6) + ' CHK';
  } catch { return wei + ' wei'; }
}

export default function Tx() {
  const { hash } = useParams<{ hash: string }>();
  const [data, setData] = useState<TxResponse | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!hash) return;
    api.tx(hash).then(d => { setData(d); setErr(''); }).catch(e => setErr(String(e)));
  }, [hash]);

  if (err) return (
    <div className="space-y-4">
      <Link
        to="/blocks"
        className="inline-flex items-center gap-1.5 text-sm text-slate-500 dark:text-gray-400 hover:text-slate-800 dark:hover:text-white transition-colors"
      >
        <ArrowLeft size={14} strokeWidth={2} /> Blocks
      </Link>
      <p className="text-sm text-red-600 dark:text-red-400">Transaction not found: {err}</p>
    </div>
  );

  if (!data) return (
    <div className="flex items-center gap-2 text-sm text-slate-500 dark:text-gray-400">
      <span className="size-1.5 rounded-full bg-indigo-500 animate-pulse" />
      Loading…
    </div>
  );

  const statusChip: Record<string, { color: 'green' | 'red' | 'amber'; Icon: typeof CheckCircle2 }> = {
    success:  { color: 'green', Icon: CheckCircle2 },
    reverted: { color: 'red',   Icon: XCircle },
    pending:  { color: 'amber', Icon: Clock },
  };
  const st = statusChip[data.status] ?? statusChip.pending;

  const rows: [string, string, boolean][] = [
    ['Hash',      data.hash,                true],
    ['Block',     `#${data.block_height}`,  false],
    ['From',      data.from,                true],
    ['To',        data.to || '—',           true],
    ['Value',     weiToChk(data.value),     false],
    ['Gas Used',  data.gas_used,            true],
    ['Gas Price', `${data.gas_price} wei`,  true],
    ['Nonce',     String(data.nonce),       false],
  ];

  return (
    <div className="space-y-6">
      <Link
        to={`/block/${data.block_height}`}
        className="inline-flex items-center gap-1.5 text-sm text-slate-500 dark:text-gray-400 hover:text-slate-800 dark:hover:text-white transition-colors"
      >
        <ArrowLeft size={14} strokeWidth={2} />
        Block #{data.block_height}
      </Link>

      <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
        <div className="bg-gradient-to-tr from-gray-900 to-gray-800 px-6 py-5 flex items-center justify-between">
          <h6 className="text-sm font-semibold text-white">Transaction Details</h6>
          <Chip color={st.color}>
            <st.Icon size={9} strokeWidth={2.5} />
            {data.status}
          </Chip>
        </div>
        <div className="bg-white dark:bg-gray-800 overflow-x-auto px-0 pt-0 pb-2">
          <table className="w-full table-auto">
            <tbody>
              {rows.map(([label, value, mono], i, arr) => (
                <tr key={label}>
                  <td className={`py-3 px-5 w-32 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{label}</span>
                  </td>
                  <td className={`py-3 px-5 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    {label === 'Block' ? (
                      <Link
                        to={`/block/${data.block_height}`}
                        className="text-sm font-semibold text-indigo-600 dark:text-indigo-400 hover:underline tabular-nums"
                      >
                        {value}
                      </Link>
                    ) : label === 'From' ? (
                      <Link
                        to={`/wallet?addr=${data.from}`}
                        className="font-mono text-xs text-indigo-600 dark:text-indigo-400 hover:underline break-all"
                      >
                        {value}
                      </Link>
                    ) : label === 'To' && data.to ? (
                      <Link
                        to={`/wallet?addr=${data.to}`}
                        className="font-mono text-xs text-indigo-600 dark:text-indigo-400 hover:underline break-all"
                      >
                        {value}
                      </Link>
                    ) : (
                      <span className={`text-slate-700 dark:text-white break-all ${mono ? 'font-mono text-xs' : 'text-sm font-medium'}`}>
                        {value}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
