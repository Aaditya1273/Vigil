'use client'

import { useEffect, useState } from 'react'
import { Network, AlertTriangle, Search, ShieldAlert } from 'lucide-react'
import { EntityGraph, GraphContext } from '@/components/EntityGraph'

interface OntologyResult {
  query: string
  entity_paths: string[]
  graph_context: GraphContext
}

interface EntityProfile {
  name: string
  aliases: string[]
  alias_paths: string[]
  alias_contexts: string[]
  policy_paths: string[]
  contradiction_paths: string[]
  contradiction_contexts: string[]
  trust_paths: string[]
  query_time_ms: number
}

// Confidence and trust scores live in the free-form sentence HydraDB
// extracted the triplet from ("...resolved with confidence 0.97 via...","
// trust score of 0.85..."), not on the triplet itself — pull the number out
// of whichever context sentence mentions this alias/source.
function confidenceFor(alias: string, contexts: string[]): number | null {
  const hit = contexts.find((c) => c.toLowerCase().includes(alias.toLowerCase()))
  const m = hit?.match(/confidence(?:\s+(?:of|is))?\s+([\d.]+)/i)
  return m ? parseFloat(m[1]) : null
}

// Parses "source --[predicate]--> target" back into its parts.
function splitPath(path: string): { source: string; target: string } | null {
  const m = path.match(/^(.*?) --\[.*?\]--> (.*)$/)
  return m ? { source: m[1], target: m[2] } : null
}

function trustScores(paths: string[]): { source: string; score: number }[] {
  const seen = new Map<string, number>()
  for (const p of paths) {
    const parts = splitPath(p)
    if (!parts) continue
    const score = parseFloat(parts.target)
    if (Number.isNaN(score)) continue
    seen.set(parts.source, Math.max(seen.get(parts.source) ?? 0, score))
  }
  return Array.from(seen, ([source, score]) => ({ source, score })).sort((a, b) => b.score - a.score)
}

export default function OntologyPage() {
  const [entity, setEntity] = useState('')
  const [result, setResult] = useState<OntologyResult | null>(null)
  const [profile, setProfile] = useState<EntityProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)

  const run = async (q: string) => {
    setLoading(true)
    setErr(null)
    try {
      if (q.trim()) {
        const r = await fetch(`/api/v1/ontology/entity?name=${encodeURIComponent(q)}`, { cache: 'no-store' })
        const body = await r.json()
        if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`)
        setProfile(body)
        setResult(null)
      } else {
        const r = await fetch('/api/v1/ontology', { cache: 'no-store' })
        const body = await r.json()
        if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`)
        setResult(body)
        setProfile(null)
      }
    } catch (e: any) {
      setErr(e.message || 'request failed')
      setResult(null)
      setProfile(null)
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
            placeholder="Look up a person, e.g. Sam or Jordan, or leave blank for the full ontology"
            className="w-full pl-9 pr-3 py-2.5 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200 focus:border-orange-400"
          />
        </div>
        <button type="submit" className="btn-orange" disabled={loading}>
          {loading ? 'Querying…' : 'Look up'}
        </button>
      </form>

      {err && (
        <div className="px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-center gap-2 mb-6">
          <AlertTriangle className="w-4 h-4" />
          {err}
        </div>
      )}

      {profile && (
        <>
          <div className="card p-5 mb-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Canonical name</p>
                <h2 className="text-xl font-bold text-gray-900 mt-1">{profile.name}</h2>
              </div>
              <p className="text-xs text-gray-400">{Math.round(profile.query_time_ms)}ms · 5 graph queries</p>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-4 mb-6">
            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100">
                <h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">
                  Resolved aliases ({profile.aliases.length})
                </h3>
              </div>
              <ul className="p-4 space-y-2">
                {profile.aliases.map((a, i) => {
                  const conf = confidenceFor(a, profile.alias_contexts)
                  return (
                    <li key={i} className="flex items-center justify-between text-xs">
                      <span className="font-mono text-gray-700">{a}</span>
                      {conf !== null && (
                        <span className="pill pill-gray font-mono">{conf.toFixed(2)}</span>
                      )}
                    </li>
                  )
                })}
                {profile.aliases.length === 0 && <li className="text-xs text-gray-400">No aliases resolved</li>}
              </ul>
            </div>

            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100 flex items-center gap-2">
                <ShieldAlert className="w-3.5 h-3.5 text-red-600" />
                <h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">
                  Contradictory records ({profile.contradiction_contexts.filter((c) => c.toLowerCase().includes('contradict')).length})
                </h3>
              </div>
              <ul className="p-4 space-y-2">
                {profile.contradiction_contexts.filter((c) => c.toLowerCase().includes('contradict')).map((c, i) => (
                  <li key={i} className="text-xs bg-red-50 text-red-700 border border-red-100 rounded-lg px-2 py-1.5">{c}</li>
                ))}
                {profile.contradiction_contexts.filter((c) => c.toLowerCase().includes('contradict')).length === 0 && (
                  <li className="text-xs text-gray-400">No contradictions found</li>
                )}
              </ul>
            </div>

            <div className="card overflow-hidden">
              <div className="px-5 py-3 border-b border-gray-100"><h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">Source trust</h3></div>
              <ul className="p-4 space-y-2">
                {trustScores(profile.trust_paths).map(({ source, score }, i) => (
                  <li key={i} className="flex items-center gap-2 text-xs">
                    <span className="font-mono text-gray-600 w-28 truncate">{source}</span>
                    <div className="flex-1 progress-track h-1.5">
                      <div className="h-full rounded-full bg-orange-400" style={{ width: `${score * 100}%` }} />
                    </div>
                    <span className="font-mono text-gray-500 w-10 text-right">{score.toFixed(2)}</span>
                  </li>
                ))}
                {trustScores(profile.trust_paths).length === 0 && <li className="text-xs text-gray-400">No trust scores found</li>}
              </ul>
            </div>
          </div>

          {profile.policy_paths.length > 0 && (
            <div className="card overflow-hidden mb-6">
              <div className="px-5 py-3 border-b border-gray-100"><h3 className="text-xs font-semibold text-gray-800 uppercase tracking-wide">Entity type & policy signal</h3></div>
              <ul className="p-4 space-y-1">
                {profile.policy_paths.map((p, i) => <li key={i} className="text-xs font-mono text-gray-600">{p}</li>)}
              </ul>
            </div>
          )}
        </>
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
