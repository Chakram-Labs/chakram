import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type Token, type TokenTransfer, type TokenHolder, formatWei } from '../api/client'
import { Card, CardHeader, KvRow, Table, EmptyRow, Pagination, Spinner, ErrorMsg, Tabs, Hash, Chip } from '../components/ui'

export default function TokenDetail() {
  const { address } = useParams<{ address: string }>()
  const [token, setToken] = useState<Token | null>(null)
  const [tab, setTab] = useState('Transfers')
  const [transfers, setTransfers] = useState<TokenTransfer[]>([])
  const [ttTotal, setTtTotal] = useState(0)
  const [ttPage, setTtPage] = useState(1)
  const [holders, setHolders] = useState<TokenHolder[]>([])
  const [hTotal, setHTotal] = useState(0)
  const [hPage, setHPage] = useState(1)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.token(address!).then(setToken).catch(e => setErr(String(e)))
  }, [address])

  useEffect(() => {
    api.tokenTransfers(address!, 25, ttPage)
      .then(r => { setTransfers(r.transfers ?? []); setTtTotal(r.total) })
  }, [address, ttPage])

  useEffect(() => {
    api.tokenHolders(address!, 25, hPage)
      .then(r => { setHolders(r.holders ?? []); setHTotal(r.total) })
  }, [address, hPage])

  if (err) return <ErrorMsg msg={err} />
  if (!token) return <Spinner />

  return (
    <div className="fade-up space-y-5">
      <div className="flex items-center gap-3 mb-2">
        <Link to="/tokens" className="text-xs text-zinc-500 hover:text-white transition-colors">← Tokens</Link>
        <span className="text-zinc-700">/</span>
        <h2 className="font-display text-xl font-bold">{token.name || 'Token'}</h2>
        {token.symbol && <Chip color="zinc">{token.symbol}</Chip>}
        <Chip color="amber">{token.type}</Chip>
      </div>

      <Card>
        <CardHeader title="Token Info" />
        <dl>
          <KvRow label="Address">
            <Link to={`/address/${token.address}`} className="font-mono text-xs text-lime hover:underline">{token.address}</Link>
          </KvRow>
          <KvRow label="Name">{token.name || '—'}</KvRow>
          <KvRow label="Symbol">{token.symbol || '—'}</KvRow>
          {token.type === 'ERC-20' && <KvRow label="Decimals">{token.decimals}</KvRow>}
          <KvRow label="Total Supply">
            {token.total_supply !== '0' ? formatWei(token.total_supply, token.decimals) : '—'} {token.symbol}
          </KvRow>
          <KvRow label="Holders">{token.holder_count.toLocaleString()}</KvRow>
          <KvRow label="Transfers" border={false}>{token.transfer_count.toLocaleString()}</KvRow>
        </dl>
      </Card>

      <div>
        <div className="mb-4">
          <Tabs tabs={['Transfers', 'Holders']} active={tab} onChange={setTab} />
        </div>

        {tab === 'Transfers' && (
          <Card>
            <Table headers={['Tx', 'From', 'To', token.type === 'ERC-721' ? 'Token ID' : 'Amount']}>
              {transfers.length === 0
                ? <EmptyRow cols={4} msg="No transfers yet" />
                : transfers.map(tt => (
                  <tr key={tt.id} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3"><Hash value={tt.tx_hash} link={`/tx/${tt.tx_hash}`} /></td>
                    <td className="px-5 py-3"><Hash value={tt.from} link={`/address/${tt.from}`} /></td>
                    <td className="px-5 py-3"><Hash value={tt.to} link={`/address/${tt.to}`} /></td>
                    <td className="px-5 py-3 text-xs text-zinc-300">
                      {tt.token_id ? `#${tt.token_id}` : formatWei(tt.value, token.decimals)} {!tt.token_id && token.symbol}
                    </td>
                  </tr>
                ))
              }
            </Table>
            <Pagination page={ttPage} total={ttTotal} limit={25} onPage={setTtPage} />
          </Card>
        )}

        {tab === 'Holders' && (
          <Card>
            <Table headers={['#', 'Address', 'Balance', 'Share']}>
              {holders.length === 0
                ? <EmptyRow cols={4} msg="No holders" />
                : holders.map((h, i) => {
                  const rank = (hPage - 1) * 25 + i + 1
                  return (
                    <tr key={h.address} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                      <td className="px-5 py-3 text-xs text-zinc-500">{rank}</td>
                      <td className="px-5 py-3"><Hash value={h.address} link={`/address/${h.address}`} chars={12} /></td>
                      <td className="px-5 py-3 text-sm text-zinc-200">{formatWei(h.balance, token.decimals)} {token.symbol}</td>
                      <td className="px-5 py-3 text-xs text-zinc-500">—</td>
                    </tr>
                  )
                })
              }
            </Table>
            <Pagination page={hPage} total={hTotal} limit={25} onPage={setHPage} />
          </Card>
        )}
      </div>
    </div>
  )
}
