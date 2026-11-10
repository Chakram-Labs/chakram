import { useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api } from '../api/client'
import { Card, CardHeader } from '../components/ui'

export default function VerifyContract() {
  const { address } = useParams<{ address: string }>()
  const [form, setForm] = useState({
    contractName: '',
    compilerVersion: '0.8.20',
    optimizerEnabled: false,
    optimizerRuns: 200,
    license: 'MIT',
    constructorArgs: '',
    source: '',
    fileName: 'Contract.sol',
  })
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<{ success: boolean; message: string } | null>(null)
  const [err, setErr] = useState('')

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!form.source.trim()) { setErr('Source code is required'); return }
    setLoading(true)
    setErr('')
    setResult(null)
    try {
      const r = await api.verifyContract(address!, {
        source_files: { [form.fileName]: form.source },
        compiler_version: form.compilerVersion,
        contract_name: form.contractName || undefined,
        optimizer_enabled: form.optimizerEnabled,
        optimizer_runs: form.optimizerRuns,
        constructor_args: form.constructorArgs || undefined,
        license: form.license,
      })
      setResult(r)
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fade-up max-w-3xl">
      <div className="flex items-center gap-3 mb-6">
        <Link to={`/address/${address}`} className="text-xs text-zinc-500 hover:text-white transition-colors">← Contract</Link>
        <span className="text-zinc-700">/</span>
        <h2 className="font-display text-xl font-bold">Verify & Publish Source</h2>
      </div>

      <Card>
        <CardHeader title="Contract Verification" subtitle={address} />
        <form onSubmit={submit} className="p-6 space-y-5">
          <div className="grid grid-cols-2 gap-4">
            <Field label="Contract Name (optional)">
              <input value={form.contractName} onChange={e => setForm(f => ({ ...f, contractName: e.target.value }))}
                     placeholder="MyToken" className={inputCls} />
            </Field>
            <Field label="Compiler Version">
              <input value={form.compilerVersion} onChange={e => setForm(f => ({ ...f, compilerVersion: e.target.value }))}
                     placeholder="0.8.20" className={inputCls} required />
            </Field>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <Field label="License">
              <select value={form.license} onChange={e => setForm(f => ({ ...f, license: e.target.value }))} className={inputCls}>
                {['MIT', 'GPL-3.0', 'LGPL-3.0', 'Apache-2.0', 'BSL-1.1', 'None'].map(l => <option key={l}>{l}</option>)}
              </select>
            </Field>
            <Field label="File Name">
              <input value={form.fileName} onChange={e => setForm(f => ({ ...f, fileName: e.target.value }))}
                     placeholder="Contract.sol" className={inputCls} />
            </Field>
          </div>

          <div className="flex items-center gap-6">
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={form.optimizerEnabled}
                     onChange={e => setForm(f => ({ ...f, optimizerEnabled: e.target.checked }))}
                     className="accent-lime" />
              <span className="text-sm text-zinc-300">Optimizer enabled</span>
            </label>
            {form.optimizerEnabled && (
              <Field label="Runs">
                <input type="number" value={form.optimizerRuns}
                       onChange={e => setForm(f => ({ ...f, optimizerRuns: Number(e.target.value) }))}
                       className={`${inputCls} w-28`} />
              </Field>
            )}
          </div>

          <Field label="Constructor Arguments (ABI-encoded hex, optional)">
            <input value={form.constructorArgs} onChange={e => setForm(f => ({ ...f, constructorArgs: e.target.value }))}
                   placeholder="0x..." className={inputCls} />
          </Field>

          <Field label="Solidity Source Code">
            <textarea value={form.source} onChange={e => setForm(f => ({ ...f, source: e.target.value }))}
                      rows={16}
                      placeholder="// SPDX-License-Identifier: MIT&#10;pragma solidity ^0.8.20;&#10;&#10;contract MyToken { ... }"
                      className={`${inputCls} font-mono text-xs resize-y`} required />
          </Field>

          {err && <p className="text-sm text-red-400">{err}</p>}

          {result && (
            <div className={`rounded-xl border px-4 py-3 text-sm ${result.success
              ? 'border-lime/25 bg-lime/5 text-lime'
              : 'border-red-500/25 bg-red-500/5 text-red-400'}`}>
              {result.success ? '✓ ' : '✗ '}{result.message}
              {result.success && (
                <Link to={`/address/${address}`} className="ml-3 underline">View contract →</Link>
              )}
            </div>
          )}

          <button type="submit" disabled={loading}
                  className="flex items-center gap-2 px-6 py-2.5 rounded-xl bg-lime text-black text-sm font-semibold hover:bg-lime/90 transition-colors disabled:opacity-50">
            {loading && <div className="w-4 h-4 border-2 border-black/20 border-t-black rounded-full animate-spin" />}
            {loading ? 'Verifying…' : 'Verify & Publish'}
          </button>
        </form>
      </Card>
    </div>
  )
}

const inputCls = 'w-full h-9 px-3 rounded-xl bg-s2 border border-white/[0.07] text-sm text-zinc-200 placeholder:text-zinc-600 focus:outline-none focus:border-lime/40 transition-colors'

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <label className="text-[11px] font-bold uppercase tracking-widest text-zinc-500">{label}</label>
      {children}
    </div>
  )
}
