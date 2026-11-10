import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type Tx, formatWei } from '../api/client'
import { Card, CardHeader, Table, EmptyRow, Pagination, Spinner, ErrorMsg, StatusBadge, Hash } from '../components/ui'

export default function Txs() {
  const [params, setParams] = useSearchParams()
  const page = Number(params.get('page') ?? 1)
  const [txs, setTxs] = useState<Tx[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    api.txs(25, page)
      .then(r => { setTxs(r.txs ?? []); setTotal(r.total); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [page])

  return (
    <div className="fade-up">
      <h2 className="font-display text-xl font-bold mb-6">Transactions</h2>
      {err && <ErrorMsg msg={err} />}
      <Card>
        <CardHeader title="All Transactions" subtitle={`${total.toLocaleString()} total`} />
        {loading ? <Spinner /> : (
          <>
            <Table headers={['Hash', 'Block', 'From', 'To', 'Value', 'Gas', 'Status']}>
              {txs.length === 0
                ? <EmptyRow cols={7} msg="No transactions yet" />
                : txs.map(tx => (
                  <tr key={tx.hash} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3"><Hash value={tx.hash} link={`/tx/${tx.hash}`} /></td>
                    <td className="px-5 py-3">
                      <Link to={`/block/${tx.block_height}`} className="text-lime text-xs hover:underline">
                        #{tx.block_height.toLocaleString()}
                      </Link>
                    </td>
                    <td className="px-5 py-3"><Hash value={tx.from} link={`/address/${tx.from}`} /></td>
                    <td className="px-5 py-3">
                      {tx.to
                        ? <Hash value={tx.to} link={`/address/${tx.to}`} />
                        : <span className="text-xs text-zinc-500">contract</span>}
                    </td>
                    <td className="px-5 py-3 text-xs text-zinc-300 whitespace-nowrap">{formatWei(tx.value)} CHK</td>
                    <td className="px-5 py-3 text-xs text-zinc-500">{tx.gas_used.toLocaleString()}</td>
                    <td className="px-5 py-3"><StatusBadge status={tx.status} /></td>
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
