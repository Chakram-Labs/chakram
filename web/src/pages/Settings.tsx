import { useState, useEffect } from 'react';
import { Card } from '../components/ui';
import { GETH_RPC, CHAIN_ID, EXPLORER_URL, IS_LOCAL, api } from '../api/client';
import { CHAKRAM_CHAIN, getEthProvider } from '../lib/staking';
import { Copy, Check, Terminal } from 'lucide-react';

function Row({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard.writeText(value).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); });
  }
  return (
    <div className="flex items-center justify-between py-3 px-6 border-b border-white/5 last:border-0">
      <span className="text-[11px] font-bold uppercase tracking-wider text-gray-500">{label}</span>
      <div className="flex items-center gap-2">
        <span className={`text-sm text-white ${mono ? 'font-mono text-xs' : 'font-medium'}`}>{value}</span>
        <button onClick={copy} className="text-gray-600 hover:text-gray-300 transition-colors">
          {copied ? <Check size={12} className="text-lime-400" /> : <Copy size={12} />}
        </button>
      </div>
    </div>
  );
}

async function addToMetaMask() {
  const eth = getEthProvider();
  if (!eth) { alert('MetaMask not detected.'); return; }
  try {
    await eth.request({ method: 'wallet_addEthereumChain', params: [CHAKRAM_CHAIN] });
  } catch (err: any) {
    if (err?.code === 4001) return; // user rejected
    if (err?.code === -32601) {
      alert(`Your wallet does not support automatic network adding.\n\nAdd manually:\n  Network: Chakram\n  RPC: ${GETH_RPC}\n  Chain ID: ${CHAIN_ID}\n  Symbol: CHK`);
      return;
    }
    console.error(err);
  }
}

interface NodeInfo { comet_peer: string; geth_enode: string; genesis_url: string; }

export default function Settings() {
  const [contractAddr, setContractAddr] = useState('');
  const [nodeInfo, setNodeInfo] = useState<NodeInfo | null>(null);
  useEffect(() => {
    api.staking().then(d => { if (d.enabled && d.contract_addr) setContractAddr(d.contract_addr); }).catch(() => {});
    fetch('/api/node-info').then(r => r.json()).then(setNodeInfo).catch(() => {});
  }, []);

  return (
    <div className="space-y-6 max-w-2xl mt-10">
      {IS_LOCAL && (
        <div className="rounded-xl border border-lime-400/20 bg-lime-400/5 px-5 py-4 flex items-start gap-3">
          <span className="text-lime-400 text-lg leading-none mt-0.5">●</span>
          <div>
            <p className="text-sm font-semibold text-lime-400">Running on your own node</p>
            <p className="text-xs text-gray-400 mt-1">
              This dashboard is connected to <strong className="text-white">localhost</strong> — your local Chakram node.
              All data, MetaMask RPC, and staking calls go directly to your machine, not through any third party.
            </p>
          </div>
        </div>
      )}
      <Card title="Network Info" subtitle="Chakram chain connection details">
        <dl>
          <Row label="Network Name"   value="Chakram" />
          <Row label="Chain ID"       value={String(CHAIN_ID)} />
          <Row label="Currency"       value="CHK" />
          <Row label="RPC URL"        value={GETH_RPC} mono />
          <Row label="Explorer"       value={EXPLORER_URL} mono />
        </dl>
        <div className="px-6 py-5 border-t border-white/5">
          <p className="text-xs text-gray-500 mb-4">
            Add Chakram to MetaMask in one click. This will configure the RPC, chain ID, and currency symbol automatically.
          </p>
          <button
            onClick={addToMetaMask}
            className="inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-lime-300 transition-colors"
          >
            + Add Chakram to MetaMask
          </button>
        </div>
      </Card>

      <Card title="Contract Addresses" subtitle="Deployed contracts on Chakram">
        <dl>
          <Row label="StakingRegistry" value={contractAddr || 'Loading…'} mono />
        </dl>
      </Card>

      {nodeInfo && (
        <Card title="Run a Full Node" subtitle="Connect to this network — anyone can sync and become a validator">
          <div className="px-6 py-5 space-y-5">

            {/* Option A: standalone binary */}
            <div>
              <p className="text-xs font-semibold text-gray-300 mb-1">Option A — Single binary (Windows / Mac / Linux, no Docker)</p>
              <p className="text-xs text-gray-500 mb-2">
                Download <code className="text-lime-400">chakram-node</code> for your platform, then run:
              </p>
              <div className="rounded-lg bg-gray-900 border border-white/5 px-4 py-3 font-mono text-xs text-lime-400 flex items-start gap-3">
                <Terminal size={13} className="shrink-0 mt-0.5 text-gray-500" />
                <span>./chakram-node</span>
              </div>
              <p className="text-xs text-gray-500 mt-2">
                That's it. geth is downloaded automatically on first run, genesis is fetched, and your browser opens to the status page.
              </p>
            </div>

            {/* Option B: Docker */}
            <div className="border-t border-white/5 pt-4">
              <p className="text-xs font-semibold text-gray-300 mb-1">Option B — Docker</p>
              <div className="rounded-lg bg-gray-900 border border-white/5 px-4 py-3 font-mono text-xs text-lime-400 flex items-start gap-3">
                <Terminal size={13} className="shrink-0 mt-0.5 text-gray-500" />
                <span>bash docker/setup.sh --mode fullnode --network-url https://node.chakram.one</span>
              </div>
            </div>

            {/* Manual config */}
            <div className="border-t border-white/5 pt-4">
              <p className="text-xs text-gray-500 mb-2">Manual connection details:</p>
              <div className="rounded-xl overflow-hidden border border-white/5">
                <table className="w-full table-auto">
                  <tbody>
                    {([
                      ['CometBFT Peer', nodeInfo.comet_peer],
                      ['Geth Enode',    nodeInfo.geth_enode],
                      ['Genesis URL',   nodeInfo.genesis_url],
                    ] as [string, string][]).map(([label, value], i, arr) => (
                      <tr key={label}>
                        <td className={`py-3 px-5 w-36 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                          <span className="text-[11px] font-bold uppercase tracking-wider text-gray-500">{label}</span>
                        </td>
                        <td className={`py-3 px-5 ${i < arr.length - 1 ? 'border-b border-white/5' : ''}`}>
                          <span className="font-mono text-xs text-white break-all">{value}</span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>

          </div>
        </Card>
      )}
    </div>
  );
}
