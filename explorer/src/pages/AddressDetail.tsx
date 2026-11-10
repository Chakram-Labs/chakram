import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type Tx, type TokenTransfer, type AddressToken, formatWei } from '../api/client'
import { Card, CardHeader, KvRow, Table, EmptyRow, Pagination, Spinner, ErrorMsg, StatusBadge, Hash, Tabs, Chip } from '../components/ui'

export default function AddressDetail() {
  const { address } = useParams<{ address: string }>()
  const [info, setInfo] = useState<any>(null)
  const [tab, setTab] = useState('Transactions')
  const [txs, setTxs] = useState<Tx[]>([])
  const [txTotal, setTxTotal] = useState(0)
  const [txPage, setTxPage] = useState(1)
  const [transfers, setTransfers] = useState<TokenTransfer[]>([])
  const [ttTotal, setTtTotal] = useState(0)
  const [ttPage, setTtPage] = useState(1)
  const [tokens, setTokens] = useState<AddressToken[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    api.address(address!)
      .then(r => { setInfo(r); setErr('') })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [address])

  useEffect(() => {
    api.addressTxs(address!, 25, txPage)
      .then(r => { setTxs(r.txs ?? []); setTxTotal(r.total) })
  }, [address, txPage])

  useEffect(() => {
    api.addressTokenTransfers(address!, 25, ttPage)
      .then(r => { setTransfers(r.transfers ?? []); setTtTotal(r.total) })
  }, [address, ttPage])

  useEffect(() => {
    api.addressTokens(address!)
      .then(r => setTokens(r.tokens ?? []))
  }, [address])

  if (loading) return <Spinner />
  if (err) return <ErrorMsg msg={err} />

  const tabs = ['Transactions', 'Token Transfers', 'Tokens Held']
  if (info?.is_contract) tabs.push('Contract')

  return (
    <div className="fade-up space-y-5">
      <h2 className="font-display text-xl font-bold">Address</h2>

      {/* Info card */}
      <Card>
        <CardHeader
          title={info?.is_contract ? (info.contract?.name || 'Contract') : 'Wallet'}
          action={
            <div className="flex items-center gap-2">
              {info?.is_contract && <Chip color="lime">Contract</Chip>}
              {info?.token && <Chip color="amber">{info.token.type}</Chip>}
              {info?.contract?.is_verified && <Chip color="lime">✓ Verified</Chip>}
            </div>
          }
        />
        <dl>
          <KvRow label="Address">
            <span className="font-mono text-xs text-white break-all">{address}</span>
          </KvRow>
          <KvRow label="Balance">
            <span className="font-semibold">{formatWei(info?.balance ?? '0')} CHK</span>
          </KvRow>
          <KvRow label="Transactions">{(info?.tx_count ?? 0).toLocaleString()}</KvRow>
          {info?.is_contract && info.contract && (
            <>
              {info.contract.creator && (
                <KvRow label="Creator">
                  <Link to={`/address/${info.contract.creator}`} className="font-mono text-xs text-lime hover:underline">
                    {info.contract.creator}
                  </Link>
                </KvRow>
              )}
              {info.contract.creation_tx && (
                <KvRow label="Creation Tx">
                  <Link to={`/tx/${info.contract.creation_tx}`} className="font-mono text-xs text-lime hover:underline">
                    {info.contract.creation_tx}
                  </Link>
                </KvRow>
              )}
              {info.contract.compiler_version && (
                <KvRow label="Compiler">{info.contract.compiler_version}</KvRow>
              )}
            </>
          )}
          {info?.token && (
            <>
              <KvRow label="Token Name">{info.token.name} ({info.token.symbol})</KvRow>
              <KvRow label="Decimals">{info.token.decimals}</KvRow>
              <KvRow label="Total Supply">{formatWei(info.token.total_supply, info.token.decimals)} {info.token.symbol}</KvRow>
              <KvRow label="Holders" border={false}>{info.token.holder_count.toLocaleString()}</KvRow>
            </>
          )}
        </dl>
      </Card>

      {/* Tabs */}
      <div>
        <div className="mb-4">
          <Tabs tabs={tabs} active={tab} onChange={setTab} />
        </div>

        {tab === 'Transactions' && (
          <Card>
            <Table headers={['Hash', 'Block', 'From', 'To', 'Value', 'Status']}>
              {txs.length === 0
                ? <EmptyRow cols={6} msg="No transactions" />
                : txs.map(tx => (
                  <tr key={tx.hash} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3"><Hash value={tx.hash} link={`/tx/${tx.hash}`} /></td>
                    <td className="px-5 py-3">
                      <Link to={`/block/${tx.block_height}`} className="text-lime text-xs hover:underline">#{tx.block_height.toLocaleString()}</Link>
                    </td>
                    <td className="px-5 py-3">
                      <span className={tx.from.toLowerCase() === address?.toLowerCase() ? 'text-white' : ''}>
                        <Hash value={tx.from} link={`/address/${tx.from}`} />
                      </span>
                    </td>
                    <td className="px-5 py-3">
                      {tx.to ? <Hash value={tx.to} link={`/address/${tx.to}`} /> : <span className="text-xs text-zinc-500">contract</span>}
                    </td>
                    <td className="px-5 py-3 text-xs text-zinc-300 whitespace-nowrap">{formatWei(tx.value)} CHK</td>
                    <td className="px-5 py-3"><StatusBadge status={tx.status} /></td>
                  </tr>
                ))
              }
            </Table>
            <Pagination page={txPage} total={txTotal} limit={25} onPage={setTxPage} />
          </Card>
        )}

        {tab === 'Token Transfers' && (
          <Card>
            <Table headers={['Tx', 'Token', 'From', 'To', 'Amount']}>
              {transfers.length === 0
                ? <EmptyRow cols={5} msg="No token transfers" />
                : transfers.map(tt => (
                  <tr key={tt.id} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3"><Hash value={tt.tx_hash} link={`/tx/${tt.tx_hash}`} /></td>
                    <td className="px-5 py-3"><Hash value={tt.token_address} link={`/token/${tt.token_address}`} /></td>
                    <td className="px-5 py-3"><Hash value={tt.from} link={`/address/${tt.from}`} /></td>
                    <td className="px-5 py-3"><Hash value={tt.to} link={`/address/${tt.to}`} /></td>
                    <td className="px-5 py-3 text-xs text-zinc-300">
                      {tt.token_id ? `#${tt.token_id}` : tt.value}
                    </td>
                  </tr>
                ))
              }
            </Table>
            <Pagination page={ttPage} total={ttTotal} limit={25} onPage={setTtPage} />
          </Card>
        )}

        {tab === 'Tokens Held' && (
          <Card>
            <Table headers={['Token', 'Symbol', 'Type', 'Balance']}>
              {tokens.length === 0
                ? <EmptyRow cols={4} msg="No tokens held" />
                : tokens.map(t => (
                  <tr key={t.token_address} className="border-b border-white/[0.04] last:border-0 hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3">
                      <Link to={`/token/${t.token_address}`} className="text-lime text-sm hover:underline">
                        {t.name || <Hash value={t.token_address} />}
                      </Link>
                    </td>
                    <td className="px-5 py-3 text-sm text-zinc-300">{t.symbol}</td>
                    <td className="px-5 py-3"><Chip color="zinc">{t.type}</Chip></td>
                    <td className="px-5 py-3 text-sm text-zinc-200">{formatWei(t.balance, t.decimals)} {t.symbol}</td>
                  </tr>
                ))
              }
            </Table>
          </Card>
        )}

        {tab === 'Contract' && info?.is_contract && (
          <ContractTab address={address!} />
        )}
      </div>
    </div>
  )
}

function ContractTab({ address }: { address: string }) {
  const [contract, setContract] = useState<any>(null)

  useEffect(() => {
    api.contract(address).then(setContract).catch(() => {})
  }, [address])

  if (!contract) return <Spinner />

  return (
    <div className="space-y-4">
      {contract.is_verified ? (
        <>
          <div className="flex items-center gap-3">
            <Chip color="lime">✓ Source Verified</Chip>
            <span className="text-xs text-zinc-500">Compiler: {contract.compiler_version}</span>
            {contract.license && <span className="text-xs text-zinc-500">License: {contract.license}</span>}
          </div>
          {contract.source_json && (
            <Card>
              <CardHeader title="Source Code" />
              <div className="p-5">
                {Object.entries(JSON.parse(contract.source_json) as Record<string, string>).map(([file, src]) => (
                  <div key={file} className="mb-4">
                    <p className="text-xs font-mono text-lime mb-2">{file}</p>
                    <pre className="bg-s2 rounded-xl p-4 text-[11px] font-mono text-zinc-300 overflow-x-auto max-h-96 overflow-y-auto">
                      {src}
                    </pre>
                  </div>
                ))}
              </div>
            </Card>
          )}
        </>
      ) : (
        <div className="rounded-xl border border-white/[0.07] bg-surface px-6 py-5">
          <p className="text-sm text-zinc-400 mb-4">Contract source not verified.</p>
          <Link to={`/contract/${address}/verify`}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-lime text-black text-sm font-semibold hover:bg-lime/90 transition-colors">
            Verify & Publish Source
          </Link>
        </div>
      )}
      {contract.bytecode && (
        <Card>
          <CardHeader title="Bytecode" />
          <div className="p-5">
            <pre className="bg-s2 rounded-xl p-4 text-[11px] font-mono text-zinc-500 overflow-x-auto max-h-48 overflow-y-auto break-all whitespace-pre-wrap">
              {contract.bytecode}
            </pre>
          </div>
        </Card>
      )}
    </div>
  )
}
