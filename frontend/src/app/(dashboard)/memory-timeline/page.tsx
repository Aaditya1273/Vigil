'use client'

import { useEffect, useState } from 'react'
import { History, AlertTriangle, Search, GitBranch, Ban } from 'lucide-react'
import { EntityGraph, GraphContext } from '@/components/EntityGraph'

interface MemoryResult {
  question: string
  entity_paths: string[]
  graph_context: GraphContext
  chunks: { source_title?: string; source_upload_time?: string; chunk_content?: string }[]
}

interface TemporalFact {
  subject: string
  has_signal: boolean
  abstain: boolean
  answer: string
  current_paths: string[]
  current_contexts: string[]
  history_paths: string[]
  history_contexts: string[]
  query_time_ms: number
}

// The seeded example built specifically to demonstrate temporal reasoning:
// an on-call fact recorded in session 4, superseded in session 22.
const DEFAULT_QUESTION = 'Has the on-call engineer changed across sessions? Which fact was superseded?'
const DEFAULT_SUBJECT = 'gym time'

function isSupersededSentence(text: string) {
  const t = text.toLowerCase()
  return t.includes('superseded') || t.includes('fact_update')
}

export default function MemoryTimelinePage() {
  const [question, setQuestion] = useState('')
  const [result, setResult] = useState<MemoryResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)

  const [subject, setSubject] = useState('')
  const [fact, setFact] = useState<TemporalFact | null>(null)
  const [factLoading, setFactLoading] = useState(true)
  const [factErr, setFactErr] = useState<string | null>(null)

  const run = async (q: string) => {
    setLoading(true)
    setErr(null)
    try {
      const url = `/api/v1/memory?question=${encodeURIComponent(q || DEFAULT_QUESTION)}`
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

  const runFact = async (s: string) => {
    const q = s.trim() || DEFAULT_SUBJECT
    setFactLoading(true)
    setFactErr(null)
    try {
      const r = await fetch(`/api/v1/memory/fact?subject=${encodeURIComponent(q)}`, { cache: 'no-store' })
      const body = await r.json()
      if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`)
      setFact(body)
    } catch (e: any) {
      setFactErr(e.message || 'request failed')
      setFact(null)
    } finally {
      setFactLoading(false)
    }
  }

  useEffect(() => { run(DEFAULT_QUESTION); runFact(DEFAULT_SUBJECT) }, [])

  // Sort chunks oldest-first so the timeline reads chronologically, like
  // GitHub blame — the most useful order for "what changed and when".
  const timeline = [...(result?.chunks || [])].sort((a, b) =>
    (a.source_upload_time || '').localeCompare(b.source_upload_time || ''))

  return (
    <div className="p-8 max-w-6xl mx-auto animate-fadeIn">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Memory Timeline</h1>
        <p className="text-sm text-gray-500 mt-1">
          agent_memory holds every fact the firewall has recorded, across every session — including
          which facts were later superseded. Temporal reasoning, not a flat log.
        </p>
      </div>

      {/* --- Fact lookup: current value + supersession history, GitHub-blame style --- */}
      <form
        onSubmit={(e) => { e.preventDefault(); runFact(subject) }}
        className="card p-5 mb-6 flex gap-2"
      >
        <div className="relative flex-1">
          <GitBranch className="w-4 h-4 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder={`Look up a fact's history, e.g. "${DEFAULT_SUBJECT}"`}
            className="w-full pl-9 pr-3 py-2.5 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200 focus:border-orange-400"
          />
        </div>
        <button type="submit" className="btn-orange" disabled={factLoading}>
          {factLoading ? 'Looking up…' : 'Trace fact'}
        </button>
      </form>

      {factErr && (
        <div className="px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-center gap-2 mb-6">
          <AlertTriangle className="w-4 h-4" />
          {factErr}
        </div>
      )}

      {fact && (
        <div className="card overflow-hidden mb-6">
          <div className="px-6 py-4 border-b border-gray-100 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-800">
              Fact: <span className="font-mono text-orange-700">{fact.subject}</span>
            </h2>
            <span className="text-xs text-gray-400">{Math.round(fact.query_time_ms)}ms · 2 graph queries</span>
          </div>

          <div className="px-6 py-4 border-b border-gray-100">
            {fact.abstain ? (
              <div className="flex items-center gap-2 text-sm text-gray-500">
                <Ban className="w-4 h-4" />
                <span className="pill pill-gray font-mono">NOT_IN_HISTORY</span>
                <span>— nothing retrieved actually mentions this subject; abstaining rather than guessing.</span>
              </div>
            ) : (
              <div>
                <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider mb-1">Current value</p>
                {fact.current_contexts.slice(0, 3).map((c, i) => (
                  <p key={i} className="text-sm text-gray-800 mb-1">{c}</p>
                ))}
                {fact.current_contexts.length === 0 && <p className="text-sm text-gray-400">No current-value text extracted.</p>}
              </div>
            )}
          </div>

          <div className="px-6 py-4">
            <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider mb-2">History</p>
            <div className="space-y-2">
              {fact.history_contexts.map((c, i) => {
                const superseded = isSupersededSentence(c)
                return (
                  <div
                    key={i}
                    className={`text-sm px-3 py-2 rounded-lg border ${superseded ? 'bg-orange-50 border-orange-200 text-orange-800' : 'bg-gray-50 border-gray-100 text-gray-600'}`}
                  >
                    {superseded && <span className="pill pill-orange mr-2 align-middle">superseded_by</span>}
                    {c}
                  </div>
                )
              })}
              {fact.history_contexts.length === 0 && <p className="text-sm text-gray-400">No history recorded.</p>}
            </div>
          </div>
        </div>
      )}

      {/* --- Free-text memory Q&A, unchanged --- */}
      <form
        onSubmit={(e) => { e.preventDefault(); run(question) }}
        className="card p-5 mb-6 flex gap-2"
      >
        <div className="relative flex-1">
          <Search className="w-4 h-4 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder={DEFAULT_QUESTION}
            className="w-full pl-9 pr-3 py-2.5 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200 focus:border-orange-400"
          />
        </div>
        <button type="submit" className="btn-orange" disabled={loading}>
          {loading ? 'Querying…' : 'Ask memory'}
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
          <div className="card p-5 mb-6">
            <div className="flex items-center gap-2 mb-4">
              <History className="w-4 h-4 text-orange-600" />
              <h2 className="text-sm font-semibold text-gray-800">Fact graph</h2>
            </div>
            <EntityGraph graphContext={result.graph_context} />
          </div>

          <div className="card overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-100">
              <h2 className="text-sm font-semibold text-gray-800">Timeline</h2>
            </div>
            <div className="divide-y divide-gray-100">
              {timeline.map((c, i) => {
                let text = ''
                try { text = JSON.parse(c.chunk_content || '{}').content?.text || '' } catch { /* leave blank */ }
                const superseded = isSupersededSentence(text)
                return (
                  <div key={i} className="px-6 py-4 flex items-start gap-4">
                    <div className="w-24 shrink-0 text-xs font-mono text-gray-400 pt-0.5">
                      {c.source_upload_time ? new Date(c.source_upload_time).toLocaleTimeString() : ''}
                    </div>
                    <div className="flex-1">
                      <p className="text-sm text-gray-800">{text || c.source_title}</p>
                      {superseded && <span className="pill pill-orange mt-1 inline-block">superseded</span>}
                    </div>
                  </div>
                )
              })}
              {timeline.length === 0 && (
                <div className="py-16 text-center text-sm text-gray-400">No memory facts recorded yet.</div>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
