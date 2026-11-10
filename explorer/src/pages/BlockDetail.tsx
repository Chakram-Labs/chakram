import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type Block, type Tx, formatTime, formatWei } from '../api/client'
import { Card, CardHeader, KvRow, Table, EmptyRow, Spinner, ErrorMsg, StatusBadge, Hash } from '../components/ui'

export default function BlockDetail() {
  const { height } = useParams<{ height: string }>()
  const [block, setBlock] = useState<Block | null>(null)
  const [txs, setTxs] = useState<Tx[]>([])
  const [latestTime, setLatestTime] = useState<number | undefined>()
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    Promise.all([api.block(height!), api.blockTxs(height!), api.latestBlock()])
      .then(([b, t, latest]) => { setBlock(b); setTxs(t.txs ?? []); setLatestTime(latest?.time); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [height])

  if (loading) return <Spinner />
  if (err) return <ErrorMsg msg={err} />
  if (!block) return <ErrorMsg msg="Block not found" />

  const gasPercent = block.gas_limit > 0 ? ((block.gas_used / block.gas_limit) * 100).toFixed(1) : '0'

  return (
    <div className="fade-up space-y-5">
      <div className="flex items-center gap-3 mb-2">
        <Link to="/blocks" className="text-xs text-zinc-500 hover:text-white transition-colors">← Blocks</Link>
        <span className="text-zinc-700">/</span>
        <h2 className="font-display text-xl font-bold">Block #{block.height.toLocaleString()}</h2>
      </div>

      <Card>
        <CardHeader title="Block Details" />
        <dl>
          <KvRow label="Height">{block.height.toLocaleString()}</KvRow>
          <KvRow label="Block Hash"><Hash value={block.hash} chars={16} /></KvRow>
          <KvRow label="Parent Hash"><Hash value={block.parent_hash} chars={16} /></KvRow>
          {block.comet_hash && <KvRow label="CometBFT Hash"><Hash value={block.comet_hash} chars={16} /></KvRow>}
          <KvRow label="Timestamp">{new Date(block.time * 1000).toUTCString()} ({formatTime(block.time, latestTime)})</KvRow>
          <KvRow label="Transactions">{block.tx_count}</KvRow>
          <KvRow label="Proposer">
            {block.proposer
              ? <Link to={`/address/${block.proposer}`} className="font-mono text-xs text-lime hover:underline">{block.proposer}</Link>
              : '—'}
          </KvRow>
          <KvRow label="Gas Used / Limit">
            {block.gas_used.toLocaleString()} / {block.gas_limit.toLocaleString()}
            <span className="ml-2 text-zinc-500">({gasPercent}%)</span>
            <div className="mt-1.5 w-32 h-1 bg-s2 rounded-full overflow-hidden">
              <div className="h-full bg-lime rounded-full" style={{ width: `${gasPercent}%` }} />
            </div>
          </KvRow>
          <KvRow label="Base Fee">{block.base_fee ? `${formatWei(block.base_fee, 9)} Gwei` : '—'}</KvRow>
          {block.app_hash && <KvRow label="App Hash" border={false}><Hash value={block.app_hash} chars={16} /></KvRow>}
        </dl>
      </Card>

      <Card>
        <CardHeader title="Transactions" subtitle={`${txs.length} in this block`} />
        <Table headers={['Hash', 'From', 'To', 'Value', 'Gas', 'Status']}>
          {txs.length === 0
            ? <EmptyRow cols={6} msg="No transactions in this block" />
            : txs.map(tx => (
              <tr key={tx.hash} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                <td className="px-5 py-3"><Hash value={tx.hash} link={`/tx/${tx.hash}`} /></td>
                <td className="px-5 py-3"><Hash value={tx.from} link={`/address/${tx.from}`} /></td>
                <td className="px-5 py-3">
                  {tx.to
                    ? <Hash value={tx.to} link={`/address/${tx.to}`} />
                    : <Link to={`/address/${tx.contract_address}`} className="text-xs text-lime">Contract creation</Link>}
                </td>
                <td className="px-5 py-3 text-xs text-zinc-300">{formatWei(tx.value)} CHK</td>
                <td className="px-5 py-3 text-xs text-zinc-500">{tx.gas_used.toLocaleString()}</td>
                <td className="px-5 py-3"><StatusBadge status={tx.status} /></td>
              </tr>
            ))
          }
        </Table>
      </Card>
    </div>
  )
}
