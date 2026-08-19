'use client'

import { useEffect, useState } from 'react'
import { TrendingUp, AlertTriangle } from 'lucide-react'

export interface Forecast {
  current_cost: number
  budget: number
  burn_rate_per_min: number
  projected_total: number
  time_to_breach_seconds: number
  will_breach: boolean
  state: 'insufficient_history' | 'stable' | 'soft_limit' | 'hard_limit'
  recommend?: string
  samples: number
}

function duration(seconds: number) {
  if (!seconds || seconds <= 0) return '—'
  const m = Math.floor(seconds / 60)
  const s = Math.round(seconds % 60)
  return m > 0 ? `${m}m ${s}s` : `${s}s`
}

const STATE_LABEL: Record<Forecast['state'], string> = {
  insufficient_history: 'Insufficient history',
  stable: 'Stable',
  soft_limit: 'Approaching budget',
  hard_limit: 'Budget exhausted',
}

export function PredictiveCost({ sessionId }: { sessionId?: string }) {
  const [f, setF] = useState<Forecast | null>(null)
  const [err, setErr] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const path = sessionId
          ? `/api/v1/vigil/sessions/${encodeURIComponent(sessionId)}/forecast`
          : '/api/v1/vigil/forecast'
        const r = await fetch(path, { cache: 'no-store' })
        if (!r.ok) { setErr(true); return }
        setF(await r.json()); setErr(false)
      } catch { setErr(true) }
    }
    load()
    const t = setInterval(load, 4000)
    return () => clearInterval(t)
  }, [sessionId])

  if (err || !f) {
    return (
      <div className="card p-6">
        <div className="flex items-center gap-2 mb-2">
          <TrendingUp className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Predictive Cost</h2>
        </div>
        <p className="text-sm text-gray-500">{err ? 'Control plane unreachable' : 'Loading…'}</p>
      </div>
    )
  }

  // With fewer than two samples there is no interval to measure a rate over.
  // Saying so is the honest rendering; a projection from one point would be
  // invented.
  const thin = f.state === 'insufficient_history'

  return (
    <div className="card p-6">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <TrendingUp className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Predictive Cost</h2>
        </div>
        {f.will_breach ? (
          <span className="pill pill-red flex items-center gap-1">
            <AlertTriangle className="w-3 h-3" /> BREACH PREDICTED
          </span>
        ) : (
          <span className="pill pill-gray">{STATE_LABEL[f.state]}</span>
        )}
      </div>

      <div className="grid grid-cols-2 gap-y-3 gap-x-6">
        <Metric label="Current" value={`$${f.current_cost.toFixed(4)}`} />
        <Metric label="Budget" value={`$${f.budget.toFixed(2)}`} />
        <Metric
          label="Burn rate"
          value={thin ? '—' : `$${f.burn_rate_per_min.toFixed(4)}/min`}
          dim={thin}
        />
        <Metric
          label="Projected"
          value={thin ? '—' : `$${f.projected_total.toFixed(4)}`}
          hi={!thin && f.projected_total > f.budget}
          dim={thin}
        />
        <Metric
          label="Time to breach"
          value={thin ? '—' : duration(f.time_to_breach_seconds)}
          hi={f.time_to_breach_seconds > 0 && f.time_to_breach_seconds < 120}
          dim={thin}
        />
        <Metric label="Samples" value={String(f.samples)} dim />
      </div>

      {thin && (
        <p className="text-xs text-gray-400 mt-4">
          Fewer than two cost samples — no rate can be measured yet.
        </p>
      )}

      {f.recommend && (
        <div className="mt-4 px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-start gap-2">
          <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
          <span>Recommended: {f.recommend}</span>
        </div>
      )}

      <p className="text-[11px] text-gray-400 mt-4">
        Straight-line projection over a rolling window of recent calls. Not a model.
      </p>
    </div>
  )
}

function Metric({ label, value, hi, dim }: { label: string; value: string; hi?: boolean; dim?: boolean }) {
  return (
    <div>
      <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">{label}</p>
      <p className={`text-lg font-bold mt-0.5 font-mono ${
        hi ? 'text-red-600' : dim ? 'text-gray-400' : 'text-gray-900'
      }`}>{value}</p>
    </div>
  )
}
