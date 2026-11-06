import { useEffect, useState, FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api, AddressResponse, FaucetResponse } from '../api/client';
import { Search, Droplets, Copy, Check } from 'lucide-react';
import { Card, Chip } from '../components/ui';

function weiToEth(wei: string): string {
  try {
    const chk = Number(BigInt(wei)) / 1e18;
    return chk.toFixed(6) + ' CHK';
  } catch { return wei + ' wei'; }
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}
      className="ml-1 inline-flex items-center rounded p-0.5 text-slate-400 hover:text-slate-700 dark:hover:text-gray-200 transition-colors"
      aria-label="Copy"
    >
      {copied
        ? <Check size={12} strokeWidth={2.5} className="text-emerald-600 dark:text-emerald-400" />
        : <Copy size={12} strokeWidth={2} />
      }
    </button>
  );
}

function inputCls() {
  return 'flex-1 appearance-none rounded-lg border border-slate-200 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-slate-900 dark:text-white placeholder:text-slate-400 dark:placeholder:text-gray-500 font-mono focus:outline-none focus:ring-2 focus:ring-inset focus:ring-indigo-500 dark:focus:ring-indigo-400';
}

export default function Wallet() {
  const [params] = useSearchParams();
  const [addr, setAddr] = useState(params.get('addr') || '');
  const [input, setInput] = useState(params.get('addr') || '');
  const [data, setData] = useState<AddressResponse | null>(null);
  const [addrErr, setAddrErr] = useState('');
  const [devMode, setDevMode] = useState<boolean | null>(null);
  const [faucetAddr, setFaucetAddr] = useState('');
  const [faucetResult, setFaucetResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [faucetLoading, setFaucetLoading] = useState(false);

  useEffect(() => {
    api.faucet('0x0000').then(() => setDevMode(true)).catch(() => setDevMode(false));
  }, []);

  function lookup(e?: FormEvent) {
    e?.preventDefault();
    const a = input.trim();
    if (!a) return;
    setAddr(a); setData(null); setAddrErr('');
    api.address(a).then(d => setData(d)).catch(e => setAddrErr(String(e)));
  }

  useEffect(() => { if (addr) lookup(); }, []);

  async function sendFaucet(e: FormEvent) {
    e.preventDefault();
    setFaucetLoading(true); setFaucetResult(null);
    try {
      const r = await api.faucet(faucetAddr) as FaucetResponse;
      setFaucetResult({ ok: true, msg: 'Sent! Tx: ' + r.tx_hash });
    } catch (err) {
      setFaucetResult({ ok: false, msg: String(err) });
    } finally { setFaucetLoading(false); }
  }

  return (
    <div className="space-y-6">

      {/* Address lookup */}
      <Card title="Address Lookup" subtitle="Query balance, nonce, and contract status for any address">
        <div className="px-6 py-5">
          <form onSubmit={lookup} className="flex gap-2">
            <input
              value={input}
              onChange={e => setInput(e.target.value)}
              placeholder="0x…"
              className={inputCls()}
            />
            <button
              type="submit"
              className="inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-indigo-600 to-indigo-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-indigo-500/20 hover:shadow-indigo-500/40 transition-shadow shrink-0"
            >
              <Search size={14} strokeWidth={2} />
              Lookup
            </button>
          </form>

          {addrErr && (
            <p className="mt-3 text-sm text-red-600 dark:text-red-400">{addrErr}</p>
          )}

          {data && (
            <div className="mt-5 rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700">
              <table className="w-full table-auto">
                <tbody>
                  {([
                    ['Address', data.address, true, true],
                    ['Balance', weiToEth(data.balance), false, false],
                    ['Nonce',   String(data.nonce),    false, false],
                  ] as [string, string, boolean, boolean][]).map(([label, value, mono, copy], i, arr) => (
                    <tr key={label}>
                      <td className={`py-3 px-4 w-28 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                        <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{label}</span>
                      </td>
                      <td className={`py-3 px-4 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                        <span className={`text-slate-700 dark:text-white break-all ${mono ? 'font-mono text-xs' : 'text-sm font-semibold'}`}>
                          {value}
                        </span>
                        {copy && <CopyButton text={value} />}
                      </td>
                    </tr>
                  ))}
                  {data.code && data.code !== '0x' && (
                    <tr>
                      <td className="py-3 px-4">
                        <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">Contract</span>
                      </td>
                      <td className="py-3 px-4">
                        <Chip color="green">Yes</Chip>
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Card>

      {/* Faucet */}
      {devMode && (
        <Card>
          <div className="px-6 py-4 border-b border-slate-50 dark:border-gray-700 flex items-center gap-3">
            <h2 className="text-sm font-semibold text-slate-700 dark:text-white">Faucet</h2>
            <Chip color="amber">Dev</Chip>
          </div>
          <div className="px-6 py-5">
            <p className="text-sm text-slate-500 dark:text-gray-400 mb-4">Send 1 CHK to any address</p>
            <form onSubmit={sendFaucet} className="flex gap-2">
              <input
                value={faucetAddr}
                onChange={e => setFaucetAddr(e.target.value)}
                placeholder="0x recipient address"
                className={inputCls()}
              />
              <button
                type="submit"
                disabled={faucetLoading}
                className="inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-teal-600 to-teal-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-teal-500/20 hover:shadow-teal-500/40 transition-shadow shrink-0 disabled:opacity-50"
              >
                <Droplets size={14} strokeWidth={2} />
                {faucetLoading ? 'Sending…' : 'Send 1 CHK'}
              </button>
            </form>
            {faucetResult && (
              <p className={`mt-3 text-xs font-mono break-all ${faucetResult.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'}`}>
                {faucetResult.msg}
              </p>
            )}
          </div>
        </Card>
      )}
    </div>
  );
}
