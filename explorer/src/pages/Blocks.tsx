import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type Block, formatTime, trunc } from '../api/client'
import { Card, CardHeader, Table, EmptyRow, Pagination, Spinner, ErrorMsg } from '../components/ui'
import { Hash } from '../components/ui'

export default function Blocks() {
  const [params, setParams] = useSearchParams()
  const page = Number(params.get('page') ?? 1)
  const [blocks, setBlocks] = useState<Block[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    api.blocks(25, page)
      .then(r => { setBlocks(r.blocks ?? []); setTotal(r.total); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [page])

  return (
    <div className="fade-up">
      <h2 className="font-display text-xl font-bold mb-6">Blocks</h2>
      {err && <ErrorMsg msg={err} />}
      <Card>
        <CardHeader title="All Blocks" subtitle={`${total.toLocaleString()} blocks indexed`} />
        {loading ? <Spinner /> : (
          <>
            <Table headers={['Height', 'Hash', 'Time', 'Txs', 'Gas Used', 'Proposer']}>
              {blocks.length === 0
                ? <EmptyRow cols={6} msg="No blocks indexed yet" />
                : blocks.map((b) => (
                  <tr key={b.height} className="hover:bg-white/[0.02] transition-colors border-b border-white/[0.04] last:border-0">
                    <td className="px-5 py-3">
                      <Link to={`/block/${b.height}`} className="text-lime font-semibold text-sm hover:underline">
                        #{b.height.toLocaleString()}
                      </Link>
                    </td>
                    <td className="px-5 py-3">
                      <Hash value={b.hash} link={`/block/${b.height}`} />
                    </td>
                    <td className="px-5 py-3 whitespace-nowrap text-xs text-zinc-400">{formatTime(b.time, blocks[0]?.time)}</td>
                    <td className="px-5 py-3 text-xs text-zinc-400">{b.tx_count}</td>
                    <td className="px-5 py-3 text-xs text-zinc-400">{b.gas_used.toLocaleString()}</td>
                    <td className="px-5 py-3">
                      <span className="font-mono text-xs text-zinc-500">{trunc(b.proposer || '—', 6)}</span>
                    </td>
                  </tr>
                ))
              }
            </Table>
            <Pagination page={page} total={total} limit={25} onPage={p => setParams({ page: String(p) })} />
          </>
        )}
      </Card>
    </div>
  )
}
