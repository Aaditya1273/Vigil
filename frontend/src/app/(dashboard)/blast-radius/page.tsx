'use client'

import { useState } from 'react'
import { GitBranch, Users, Copy, AlertTriangle, Search } from 'lucide-react'
import { EntityGraph, GraphContext } from '@/components/EntityGraph'

interface BlastRadiusResult {
  package: string
  exposed_services: string[]
  maintainer_shared: string[]
  typosquats: string[]
  dependency_graph: GraphContext
  maintainer_graph: GraphContext
  typosquat_graph: GraphContext
  blast_radius_time_ms: number
}

// Real npm packages worth a click — either seeded manually
// (`vigil-cli hydra-seed`) or ingested for real by
// `scripts/ingest_npm.py` (deps.dev + npm registry, no fixtures).
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

  const flagged = (result?.typosquats.length ?? 0) > 0

  return (
    <div className="p-8 max-w-6xl mx-auto animate-fadeIn">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Blast Radius</h1>
        <p className="text-sm text-gray-500 mt-1">
          Three real HydraDB queries against code_graph — dependency exposure, maintainer overlap,
          typosquat detection — the same checks <code className="font-mono text-xs bg-gray-100 px-1 rounded">pip install</code> /
          <code className="font-mono text-xs bg-gray-100 px-1 rounded">npm install</code> run through the firewall before executing.
        </p>
      </div>

      <div className="card p-5 mb-6">
        <form onSubmit={(e) => { e.preventDefault(); run(pkg) }} className="flex gap-2">
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
          <div className="grid grid-cols-4 gap-4 mb-6">
            <div className={`stat-card ${flagged ? 'orange' : ''}`}>
              <p className="text-[11px] font-medium uppercase tracking-wider opacity-70">Verdict</p>
              <p className="text-xl font-bold mt-1">{flagged ? 'Flagged' : 'No findings'}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Exposed services</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{result.exposed_services.length}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Shared maintainers</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{result.maintainer_shared.length}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">
                Query time <span title="Real measured latency across all 3 graph_context=true queries. Plain memory retrieval (no graph context) runs closer to 200-250ms; graph-enriched structural queries like this one cost more.">ⓘ</span>
              </p>
              <p className="text-xl font-bold mt-1 text-gray-900">{Math.round(result.blast_radius_time_ms)}ms</p>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-4 mb-6">
            <div className="card p-4">
              <div className="flex items-center gap-2 mb-3">
                <GitBranch className="w-4 h-4 text-orange-600" />
                <h2 className="text-xs font-semibold text-gray-800">Dependency graph</h2>
              </div>
              <EntityGraph graphContext={result.dependency_graph} height={280} />
            </div>
            <div className="card p-4">
              <div className="flex items-center gap-2 mb-3">
                <Users className="w-4 h-4 text-orange-600" />
                <h2 className="text-xs font-semibold text-gray-800">Maintainer graph</h2>
              </div>
              <EntityGraph graphContext={result.maintainer_graph} height={280} />
            </div>
            <div className="card p-4">
              <div className="flex items-center gap-2 mb-3">
                <Copy className="w-4 h-4 text-orange-600" />
                <h2 className="text-xs font-semibold text-gray-800">Typosquat graph</h2>
              </div>
              <EntityGraph graphContext={result.typosquat_graph} height={280} />
            </div>
          </div>

          <div className="card overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-100">
              <h2 className="text-sm font-semibold text-gray-800">Exposed services</h2>
            </div>
            <table className="data-table">
              <thead><tr><th>Service / package</th><th>Exposure</th></tr></thead>
              <tbody>
                {result.exposed_services.map((s, i) => (
                  <tr key={i}><td className="font-mono text-xs">{s}</td><td><span className="pill pill-orange">transitively depends</span></td></tr>
                ))}
                {result.maintainer_shared.map((s, i) => (
                  <tr key={`m${i}`}><td className="font-mono text-xs">{s}</td><td><span className="pill pill-gray">shares maintainer</span></td></tr>
                ))}
                {result.typosquats.map((s, i) => (
                  <tr key={`t${i}`}><td className="font-mono text-xs">{s}</td><td><span className="pill pill-red">typosquat</span></td></tr>
                ))}
                {result.exposed_services.length + result.maintainer_shared.length + result.typosquats.length === 0 && (
                  <tr><td colSpan={2} className="text-gray-400 text-sm py-8 text-center">No exposure found for this package.</td></tr>
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
