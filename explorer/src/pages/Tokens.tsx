import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type Token } from '../api/client'
import { Card, CardHeader, Table, EmptyRow, Pagination, Spinner, ErrorMsg, Chip, Hash } from '../components/ui'

export default function Tokens() {
  const [params, setParams] = useSearchParams()
  const page = Number(params.get('page') ?? 1)
  const [tokens, setTokens] = useState<Token[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    api.tokens(25, page)
      .then(r => { setTokens(r.tokens ?? []); setTotal(r.total); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [page])

  return (
    <div className="fade-up">
      <h2 className="font-display text-xl font-bold mb-6">Tokens</h2>
      {err && <ErrorMsg msg={err} />}
      <Card>
        <CardHeader title="All Tokens" subtitle={`${total.toLocaleString()} tokens`} />
        {loading ? <Spinner /> : (
          <>
            <Table headers={['Token', 'Address', 'Type', 'Total Supply', 'Holders', 'Transfers']}>
              {tokens.length === 0
                ? <EmptyRow cols={6} msg="No tokens detected yet" />
                : tokens.map(t => (
                  <tr key={t.address} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3">
                      <Link to={`/token/${t.address}`} className="font-semibold text-sm text-white hover:text-lime transition-colors">
                        {t.name || '—'}
                        {t.symbol && <span className="ml-2 text-zinc-500 font-normal text-xs">{t.symbol}</span>}
                      </Link>
                    </td>
                    <td className="px-5 py-3"><Hash value={t.address} link={`/token/${t.address}`} /></td>
                    <td className="px-5 py-3"><Chip color="zinc">{t.type}</Chip></td>
                    <td className="px-5 py-3 text-xs text-zinc-300">{t.total_supply || '—'}</td>
                    <td className="px-5 py-3 text-xs text-zinc-400">{t.holder_count.toLocaleString()}</td>
                    <td className="px-5 py-3 text-xs text-zinc-400">{t.transfer_count.toLocaleString()}</td>
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
