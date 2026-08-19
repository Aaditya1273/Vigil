'use client'

import { useState } from 'react'
import { GitBranch, AlertTriangle, Search } from 'lucide-react'
import { EntityGraph, GraphContext } from '@/components/EntityGraph'

interface BlastRadiusResult {
  package: string
  entity_paths: string[]
  graph_context: GraphContext
  chunks: { source_title?: string }[]
  query_time_ms: number
  hydra_latency_ms: number
}

// Seeded demo packages worth a click — real docs ingested by
// `vigil-cli hydra-seed`, not fixtures.
const SUGGESTIONS = ['reqeusts', 'cross-env-2', 'express', 'lodash', 'left-pad']

export default function BlastRadiusPage() {
  const [pkg, setPkg] = useState('')
  const [result, setResult] = useState<BlastRadiusResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const run = async (name: string) => {
    if (!name.trim()) return
    setLoading(true)
    setErr(null)
    try {
      const r = await fetch(`/api/v1/blast-radius?package=${encodeURIComponent(name)}`, { cache: 'no-store' })
      const body = await r.json()
      if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`)
      setResult(body)
    } catch (e: any) {
      setErr(e.message || 'request failed')
      setResult(null)
    } finally {
      setLoading(false)
    }
  }

  const flagged = result?.entity_paths.some((p) => p.toLowerCase().includes('typosquat'))

  return (
    <div className="p-8 max-w-6xl mx-auto animate-fadeIn">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Blast Radius</h1>
        <p className="text-sm text-gray-500 mt-1">
          Query HydraDB&apos;s code_graph collection for a package&apos;s transitive dependency
          closure and maintainer graph — the same check <code className="font-mono text-xs bg-gray-100 px-1 rounded">pip install</code> /
          <code className="font-mono text-xs bg-gray-100 px-1 rounded">npm install</code> runs through the firewall before it executes.
        </p>
      </div>

      <div className="card p-5 mb-6">
        <form
          onSubmit={(e) => { e.preventDefault(); run(pkg) }}
          className="flex gap-2"
        >
          <div className="relative flex-1">
            <Search className="w-4 h-4 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
            <input
              value={pkg}
              onChange={(e) => setPkg(e.target.value)}
              placeholder="Package name, e.g. reqeusts"
              className="w-full pl-9 pr-3 py-2.5 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200 focus:border-orange-400"
            />
          </div>
          <button type="submit" className="btn-orange" disabled={loading}>
            {loading ? 'Querying…' : 'Check'}
          </button>
        </form>
        <div className="flex gap-2 mt-3 flex-wrap">
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              onClick={() => { setPkg(s); run(s) }}
              className="text-xs font-medium px-3 py-1.5 rounded-full bg-gray-50 text-gray-600 hover:bg-orange-50 hover:text-orange-700 transition-colors"
            >
              {s}
            </button>
          ))}
        </div>
      </div>

      {err && (
        <div className="px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-center gap-2 mb-6">
          <AlertTriangle className="w-4 h-4" />
          {err}
        </div>
      )}

      {result && (
        <>
          <div className="grid grid-cols-3 gap-4 mb-6">
            <div className={`stat-card ${flagged ? 'orange' : ''}`}>
              <p className="text-[11px] font-medium uppercase tracking-wider opacity-70">Verdict</p>
              <p className="text-xl font-bold mt-1">{flagged ? 'Flagged — typosquat' : 'No findings'}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Relationships found</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{result.entity_paths.length}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Query time</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{result.query_time_ms}ms</p>
            </div>
          </div>

          <div className="card p-5 mb-6">
            <div className="flex items-center gap-2 mb-4">
              <GitBranch className="w-4 h-4 text-orange-600" />
              <h2 className="text-sm font-semibold text-gray-800">Dependency &amp; maintainer graph</h2>
            </div>
            <EntityGraph graphContext={result.graph_context} />
          </div>

          <div className="card overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-100">
              <h2 className="text-sm font-semibold text-gray-800">Extracted relationships</h2>
            </div>
            <table className="data-table">
              <thead><tr><th>Path</th></tr></thead>
              <tbody>
                {result.entity_paths.map((p, i) => (
                  <tr key={i}><td className="font-mono text-xs">{p}</td></tr>
                ))}
                {result.entity_paths.length === 0 && (
                  <tr><td className="text-gray-400 text-sm py-8 text-center">No relationships extracted for this package.</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}

      {!result && !loading && !err && (
        <div className="py-24 text-center">
          <GitBranch className="w-10 h-10 text-gray-300 mx-auto mb-3" />
          <p className="text-sm text-gray-500">Enter a package name to query the code_graph collection.</p>
        </div>
      )}
    </div>
  )
}
