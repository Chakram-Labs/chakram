import { useEffect, useState, FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api, AddressResponse, FaucetResponse, NodeWalletResponse } from '../api/client';
import { Search, Droplets, Copy, Check, Terminal } from 'lucide-react';
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
      className="ml-1 inline-flex items-center rounded p-0.5 text-gray-600 hover:text-white transition-colors"
      aria-label="Copy"
    >
      {copied
        ? <Check size={12} strokeWidth={2.5} className="text-lime-400" />
        : <Copy size={12} strokeWidth={2} />
      }
    </button>
  );
}

const inputCls = 'flex-1 appearance-none rounded-lg border border-white/8 bg-[#161616] px-3 py-2 text-sm text-white placeholder:text-gray-600 font-mono focus:outline-none focus:ring-1 focus:ring-lime-400/30';

function NodeWalletSection() {
  const [wallet, setWallet] = useState<NodeWalletResponse | null>(null);

  useEffect(() => {
    api.nodeWallet().then(setWallet).catch(() => setWallet({ address: '', balance_wei: '0', has_key: false }));
  }, []);

  if (!wallet) return null;

  if (!wallet.has_key) {
    return (
      <Card title="Node EVM Wallet" subtitle="Your node's auto-generated Ethereum wallet">
        <div className="px-6 py-5">
          <div className="rounded-lg border border-amber-500/20 bg-amber-500/8 px-4 py-3">
            <p className="text-sm text-amber-400">No wallet found. Start chakram-node to generate it automatically.</p>
          </div>
        </div>
      </Card>
    );
  }

  const cmd = 'chakram-node wallet';

  return (
    <Card title="Node EVM Wallet" subtitle="This node's auto-generated Ethereum wallet — used for staking and block rewards">
      <div className="px-6 py-5 space-y-4">
        <div className="rounded-xl overflow-hidden border border-white/8">
          <table className="w-full table-auto">
            <tbody>
              <tr>
                <td className="py-3 px-4 w-28 border-b border-white/5">
                  <span className="text-[11px] font-bold uppercase text-gray-500">Address</span>
                </td>
                <td className="py-3 px-4 border-b border-white/5">
                  <span className="font-mono text-xs text-white break-all">{wallet.address}</span>
                  <CopyButton text={wallet.address} />
                </td>
              </tr>
              <tr>
                <td className="py-3 px-4 w-28">
                  <span className="text-[11px] font-bold uppercase text-gray-500">Balance</span>
                </td>
                <td className="py-3 px-4">
                  <span className="text-sm font-semibold text-white">{weiToEth(wallet.balance_wei)}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div className="rounded-lg border border-white/8 bg-[#0e0e0e] px-4 py-3 space-y-2">
          <div className="flex items-center gap-2 text-gray-400">
            <Terminal size={13} strokeWidth={2} />
            <span className="text-[11px] font-semibold uppercase tracking-wide">Export private key</span>
          </div>
          <p className="text-[11px] text-gray-500">
            Private keys are never exposed through the web interface. SSH into your node and run:
          </p>
          <div className="flex items-center gap-2 rounded bg-[#161616] border border-white/6 px-3 py-2">
            <code className="flex-1 font-mono text-xs text-lime-400">{cmd}</code>
            <CopyButton text={cmd} />
          </div>
        </div>

        <p className="text-[11px] text-gray-500">
          The chain owner must whitelist this address before you can register as a validator.
          Use <code className="font-mono bg-[#161616] px-1 rounded">contracts/whitelist.py --address {wallet.address}</code> on the owner's machine.
        </p>
      </div>
    </Card>
  );
}

export default function Wallet() {
  const [params] = useSearchParams();
  const [addr,  setAddr]  = useState(params.get('addr') || '');
  const [input, setInput] = useState(params.get('addr') || '');
  const [data,  setData]  = useState<AddressResponse | null>(null);
  const [addrErr,       setAddrErr]       = useState('');
  const [devMode,       setDevMode]       = useState<boolean | null>(null);
  const [faucetAddr,    setFaucetAddr]    = useState('');
  const [faucetResult,  setFaucetResult]  = useState<{ ok: boolean; msg: string } | null>(null);
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

      <NodeWalletSection />

      <Card title="Address Lookup" subtitle="Query balance, nonce, and contract status for any address">
        <div className="px-6 py-5">
          <form onSubmit={lookup} className="flex gap-2">
            <input value={input} onChange={e => setInput(e.target.value)} placeholder="0x…" className={inputCls} />
            <button
              type="submit"
              className="inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-lime-300 transition-colors shrink-0"
            >
              <Search size={14} strokeWidth={2} />
              Lookup
            </button>
          </form>

          {addrErr && <p className="mt-3 text-sm text-red-400">{addrErr}</p>}

          {data && (
            <div className="mt-5 rounded-xl overflow-hidden border border-white/8">
              <table className="w-full table-auto">
                <tbody>
                  {([
                    ['Address', data.address, true,  true],
                    ['Balance', weiToEth(data.balance), false, false],
                    ['Nonce',   String(data.nonce),    false, false],
                  ] as [string, string, boolean, boolean][]).map(([label, value, mono, copy], i, arr) => (
                    <tr key={label}>
                      <td className={`py-3 px-4 w-28 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className="text-[11px] font-bold uppercase text-gray-500">{label}</span>
                      </td>
                      <td className={`py-3 px-4 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className={`text-white break-all ${mono ? 'font-mono text-xs' : 'text-sm font-semibold'}`}>
                          {value}
                        </span>
                        {copy && <CopyButton text={value} />}
                      </td>
                    </tr>
                  ))}
                  {data.code && data.code !== '0x' && (
                    <tr>
                      <td className="py-3 px-4">
                        <span className="text-[11px] font-bold uppercase text-gray-500">Contract</span>
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

      {devMode && (
        <Card>
          <div className="px-6 py-4 border-b border-white/8 flex items-center gap-3">
            <h2 className="text-sm font-semibold text-white">Faucet</h2>
            <Chip color="amber">Dev</Chip>
          </div>
          <div className="px-6 py-5">
            <p className="text-sm text-gray-500 mb-4">Send 1 CHK to any address</p>
            <form onSubmit={sendFaucet} className="flex gap-2">
              <input
                value={faucetAddr}
                onChange={e => setFaucetAddr(e.target.value)}
                placeholder="0x recipient address"
                className={inputCls}
              />
              <button
                type="submit"
                disabled={faucetLoading}
                className="inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-lime-300 transition-colors shrink-0 disabled:opacity-50"
              >
                <Droplets size={14} strokeWidth={2} />
                {faucetLoading ? 'Sending…' : 'Send 1 CHK'}
              </button>
            </form>
            {faucetResult && (
              <p className={`mt-3 text-xs font-mono break-all ${faucetResult.ok ? 'text-lime-400' : 'text-red-400'}`}>
                {faucetResult.msg}
              </p>
            )}
          </div>
        </Card>
      )}
    </div>
  );
}
