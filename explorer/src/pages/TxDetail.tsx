import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type Tx, type Log, formatWei } from '../api/client'
import { Card, CardHeader, KvRow, Spinner, ErrorMsg, StatusBadge, Hash, Chip } from '../components/ui'

export default function TxDetail() {
  const { hash } = useParams<{ hash: string }>()
  const [tx, setTx] = useState<Tx | null>(null)
  const [logs, setLogs] = useState<Log[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    api.tx(hash!)
      .then(r => { setTx(r.tx); setLogs(r.logs ?? []); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [hash])

  if (loading) return <Spinner />
  if (err) return <ErrorMsg msg={err} />
  if (!tx) return <ErrorMsg msg="Transaction not found" />

  const isContractCreate = !tx.to && tx.contract_address

  return (
    <div className="fade-up space-y-5">
      <div className="flex items-center gap-3 mb-2">
        <Link to="/txs" className="text-xs text-zinc-500 hover:text-white transition-colors">← Transactions</Link>
        <span className="text-zinc-700">/</span>
        <h2 className="font-display text-xl font-bold">Transaction</h2>
      </div>

      <Card>
        <CardHeader title="Transaction Details" action={<StatusBadge status={tx.status} />} />
        <dl>
          <KvRow label="Tx Hash"><Hash value={tx.hash} chars={24} /></KvRow>
          <KvRow label="Block">
            <Link to={`/block/${tx.block_height}`} className="text-lime hover:underline">
              #{tx.block_height.toLocaleString()}
            </Link>
          </KvRow>
          <KvRow label="Type">
            <Chip color="zinc">Type {tx.type} {tx.type === 2 ? '(EIP-1559)' : '(Legacy)'}</Chip>
          </KvRow>
          <KvRow label="From">
            <Link to={`/address/${tx.from}`} className="font-mono text-xs text-lime hover:underline">{tx.from}</Link>
          </KvRow>
          <KvRow label={isContractCreate ? 'Contract Created' : 'To'}>
            {isContractCreate
              ? <Link to={`/address/${tx.contract_address}`} className="font-mono text-xs text-lime hover:underline">{tx.contract_address}</Link>
              : tx.to
                ? <Link to={`/address/${tx.to}`} className="font-mono text-xs text-lime hover:underline">{tx.to}</Link>
                : '—'}
          </KvRow>
          <KvRow label="Value">
            <span className="font-semibold">{formatWei(tx.value)} CHK</span>
            <span className="text-zinc-500 ml-2 text-xs">({tx.value} wei)</span>
          </KvRow>
          <KvRow label="Gas Used / Limit">
            {tx.gas_used.toLocaleString()} / {tx.gas_limit.toLocaleString()}
            {tx.gas_limit > 0 && (
              <span className="ml-2 text-zinc-500 text-xs">({((tx.gas_used / tx.gas_limit) * 100).toFixed(1)}%)</span>
            )}
          </KvRow>
          <KvRow label="Gas Price">{tx.gas_price ? `${formatWei(tx.gas_price, 9)} Gwei` : '—'}</KvRow>
          {tx.max_fee_per_gas && tx.max_fee_per_gas !== '0' && (
            <KvRow label="Max Fee">{formatWei(tx.max_fee_per_gas, 9)} Gwei</KvRow>
          )}
          <KvRow label="Nonce">{tx.nonce}</KvRow>
          <KvRow label="Input Data" border={false}>
            {tx.input && tx.input !== '0x'
              ? (
                <details className="text-right">
                  <summary className="cursor-pointer text-xs text-zinc-400 hover:text-white">
                    {tx.input.length / 2 - 1} bytes
                  </summary>
                  <pre className="mt-2 text-left font-mono text-[11px] text-zinc-400 bg-s2 rounded-xl p-4 overflow-x-auto max-h-48 overflow-y-auto whitespace-pre-wrap break-all">
                    {tx.input}
                  </pre>
                </details>
              )
              : <span className="text-zinc-500 text-xs">0x (empty)</span>}
          </KvRow>
        </dl>
      </Card>

      {/* Logs */}
      {logs.length > 0 && (
        <Card>
          <CardHeader title={`Event Logs (${logs.length})`} />
          <div className="divide-y divide-white/[0.05]">
            {logs.map(log => (
              <div key={log.id} className="px-6 py-4">
                <div className="flex items-center gap-3 mb-3">
                  <span className="text-[10px] font-bold uppercase tracking-widest text-zinc-600">Log #{log.log_index}</span>
                  <Link to={`/address/${log.address}`} className="font-mono text-xs text-lime hover:underline">{log.address}</Link>
                </div>
                <div className="space-y-1.5">
                  {[log.topic0, log.topic1, log.topic2, log.topic3].filter(Boolean).map((t, i) => (
                    <div key={i} className="flex items-start gap-3">
                      <span className="text-[10px] text-zinc-600 font-mono mt-0.5 w-14 shrink-0">topic{i}</span>
                      <span className="font-mono text-[11px] text-zinc-300 break-all">{t}</span>
                    </div>
                  ))}
                  {log.data && log.data !== '0x' && (
                    <div className="flex items-start gap-3">
                      <span className="text-[10px] text-zinc-600 font-mono mt-0.5 w-14 shrink-0">data</span>
                      <span className="font-mono text-[11px] text-zinc-400 break-all">{log.data}</span>
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  )
}
