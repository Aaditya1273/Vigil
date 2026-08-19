'use client'

import { useEffect, useState } from 'react'
import { Network, AlertTriangle, Search } from 'lucide-react'
import { EntityGraph, GraphContext } from '@/components/EntityGraph'

interface OntologyResult {
  query: string
  entity_paths: string[]
  graph_context: GraphContext
}

export default function OntologyPage() {
  const [entity, setEntity] = useState('')
  const [result, setResult] = useState<OntologyResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)

  const run = async (q: string) => {
    setLoading(true)
    setErr(null)
    try {
      const url = q.trim() ? `/api/v1/ontology?entity=${encodeURIComponent(q)}` : '/api/v1/ontology'
      const r = await fetch(url, { cache: 'no-store' })
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

  useEffect(() => { run('') }, [])

  const aliases = result?.entity_paths.filter((p) => p.includes('also known as')) || []
  const contradictions = result?.entity_paths.filter((p) => p.includes('contradicts')) || []
  const trust = result?.entity_paths.filter((p) => p.includes('trust score')) || []

  return (
    <div className="p-8 max-w-6xl mx-auto animate-fadeIn">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Ontology</h1>
        <p className="text-sm text-gray-500 mt-1">
          The enterprise knowledge graph HydraDB extracted from ingested documents — entity
          resolution, contradictory records, source trust, and policy scope.
        </p>
      </div>

      <form
        onSubmit={(e) => { e.preventDefault(); run(entity) }}
        className="card p-5 mb-6 flex gap-2"
      >
        <div className="relative flex-1">
          <Search className="w-4 h-4 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            value={entity}
            onChange={(e) => setEntity(e.target.value)}
            placeholder="Ask about an entity, e.g. Sam, or leave blank for the full ontology"
            className="w-full pl-9 pr-3 py-2.5 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200 focus:border-orange-400"
          />
        </div>
        <button type="submit" className="btn-orange" disabled={loading}>
          {loading ? 'Querying…' : 'Ask the graph'}
        </button>
      </form>

      {err && (
        <div className="px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-center gap-2 mb-6">
          <AlertTriangle className="w-4 h-4" />
          {err}
        </div>
      )}

      {result && (
        <>
          <div className="grid grid-cols-3 gap-4 mb-6">
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Merged aliases</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{aliases.length}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Contradictions</p>
              <p className={`text-xl font-bold mt-1 ${contradictions.length ? 'text-orange-600' : 'text-gray-900'}`}>{contradictions.length}</p>
            </div>
            <div className="stat-card">
              <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Trust-scored sources</p>
              <p className="text-xl font-bold mt-1 text-gray-900">{trust.length}</p>
            </div>
          </div>

          <div className="card p-5 mb-6">
            <div className="flex items-center gap-2 mb-4">
              <Network className="w-4 h-4 text-orange-600" />
              <h2 className="text-sm font-semibold text-gray-800">Entity graph</h2>
            </div>
            <EntityGraph graphContext={result.graph_context} height={480} />
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100"><h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">Aliases</h3></div>
              <ul className="p-4 space-y-2">
                {aliases.map((a, i) => <li key={i} className="text-xs font-mono text-gray-600">{a}</li>)}
                {aliases.length === 0 && <li className="text-xs text-gray-400">None found</li>}
              </ul>
            </div>
            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100"><h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">Contradictions</h3></div>
              <ul className="p-4 space-y-2">
                {contradictions.map((a, i) => <li key={i} className="text-xs font-mono text-orange-700">{a}</li>)}
                {contradictions.length === 0 && <li className="text-xs text-gray-400">None found</li>}
              </ul>
            </div>
            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100"><h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">Trust scores</h3></div>
              <ul className="p-4 space-y-2">
                {trust.map((a, i) => <li key={i} className="text-xs font-mono text-gray-600">{a}</li>)}
                {trust.length === 0 && <li className="text-xs text-gray-400">None found</li>}
              </ul>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
