'use client'

import { useEffect, useState } from 'react'
import { ShieldCheck } from 'lucide-react'

export interface Decision {
  decision: 'ALLOW' | 'PAUSE' | 'BLOCK' | 'FALLBACK'
  stage: string
  reason: string
  rule_name?: string
  tool: string
  session_id: string
  risk_score: number
  model_used?: string
  cost: number
  at: string
}

const PILL: Record<Decision['decision'], string> = {
  ALLOW: 'pill pill-green',
  BLOCK: 'pill pill-red',
  PAUSE: 'pill pill-orange',
  FALLBACK: 'pill pill-orange',
}

/** Renders how a decision was reached, so the stream shows *why* not just *what*. */
function stageLabel(stage: string) {
  switch (stage) {
    case 'intent': return 'declared intent'
    case 'forecast': return 'cost forecast'
    case 'behavior': return 'behavior'
    case 'judge': return 'AI judge'
    default: return 'deterministic'
  }
}

/**
 * Risk score is -1 when no model was consulted, which is the common case by
 * design. Showing "0" there would read as "a model looked and found no risk".
 */
function RiskCell({ score }: { score: number }) {
  if (score < 0) {
    return <span className="text-gray-400 text-xs">—</span>
  }
  const color = score >= 70 ? 'bg-red-500' : score >= 40 ? 'bg-orange-500' : 'bg-green-500'
  return (
    <div className="flex items-center gap-2">
      <span className="font-mono text-xs w-6">{score}</span>
      <div className="progress-track w-10">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${score}%` }} />
      </div>
    </div>
  )
}

export function DecisionStream({ sessionId }: { sessionId?: string }) {
  const [rows, setRows] = useState<Decision[]>([])
  const [err, setErr] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const q = sessionId ? `?session=${encodeURIComponent(sessionId)}&limit=50` : '?limit=50'
        const r = await fetch(`/api/v1/vigil/decisions${q}`, { cache: 'no-store' })
        if (!r.ok) { setErr(true); return }
        const d = await r.json()
        // Newest first for reading; the API returns oldest-first.
        setRows((d.decisions ?? []).slice().reverse())
        setErr(false)
      } catch { setErr(true) }
    }
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [sessionId])

  return (
    <div className="card overflow-hidden mt-6">
      <div className="px-6 py-4 border-b border-gray-100 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Live Decision Stream</h2>
        </div>
        <span className="text-xs text-gray-400">{rows.length} recent</span>
      </div>

      {rows.length === 0 ? (
        <div className="py-12 text-center">
          <ShieldCheck className="w-8 h-8 text-gray-300 mx-auto" />
          <p className="text-sm text-gray-500 mt-2">
            {err ? 'Control plane unreachable' : 'No decisions yet'}
          </p>
          <p className="text-xs text-gray-400 mt-1">
            {err ? 'Is the Vigil server running on :8080?' : 'Every governed tool call appears here.'}
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="data-table">
            <thead>
              <tr>
                <th>Time</th><th>Tool</th><th>Decision</th><th>Risk</th>
                <th>Cost</th><th>Decided by</th><th>Reason</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d, i) => (
                <tr key={`${d.at}-${i}`}>
                  <td className="font-mono text-xs text-gray-500">
                    {new Date(d.at).toLocaleTimeString()}
                  </td>
                  <td className="font-mono text-xs">{d.tool}</td>
                  <td><span className={PILL[d.decision]}>{d.decision}</span></td>
                  <td><RiskCell score={d.risk_score} /></td>
                  <td className="font-mono text-xs text-orange-600">${d.cost.toFixed(4)}</td>
                  <td className="text-xs text-gray-600">
                    {d.model_used || stageLabel(d.stage)}
                  </td>
                  <td className="text-xs text-gray-600 max-w-md truncate" title={d.reason}>
                    {d.rule_name ? `${d.rule_name}: ` : ''}{d.reason}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
