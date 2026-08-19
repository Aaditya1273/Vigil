'use client'

import { useEffect, useState } from 'react'
import { Cpu, AlertTriangle } from 'lucide-react'

interface ModelStat {
  model_id: string
  role: string
  requests: number
  failures: number
  fallbacks: number
  avg_latency_ms: number
  total_tokens: number
}

interface Status {
  provider: string
  configured: boolean
  roles: string[]
  models: ModelStat[]
}

const ROLE_LABEL: Record<string, string> = {
  FAST_RISK_CLASSIFIER: 'Fast triage',
  POLICY_REASONER: 'Reasoner',
  DEEP_SECURITY_REVIEWER: 'Security review',
}

export default function ModelsPage() {
  const [s, setS] = useState<Status | null>(null)
  const [err, setErr] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const r = await fetch('/api/v1/vigil/models', { cache: 'no-store' })
        if (!r.ok) { setErr(true); return }
        setS(await r.json()); setErr(false)
      } catch { setErr(true) }
    }
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [])

  const rows = s?.models ?? []
  const totals = rows.reduce(
    (a, m) => ({
      requests: a.requests + m.requests,
      fallbacks: a.fallbacks + m.fallbacks,
      failures: a.failures + m.failures,
      tokens: a.tokens + m.total_tokens,
    }),
    { requests: 0, fallbacks: 0, failures: 0, tokens: 0 },
  )

  return (
    <div className="p-8 max-w-6xl mx-auto animate-fadeIn">
      <div className="flex items-start justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Model Router</h1>
          <p className="text-sm text-gray-500 mt-1">
            Inference is consulted only when deterministic checks are uncertain.
          </p>
        </div>
        {s && (
          <span className={`pill ${s.configured ? 'pill-green' : 'pill-gray'}`}>
            {s.provider}
          </span>
        )}
      </div>

      {err && (
        <div className="px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-center gap-2 mb-6">
          <AlertTriangle className="w-4 h-4" />
          Control plane unreachable.
        </div>
      )}

      {/* No credentials is a supported configuration, not an error. Say so
          plainly rather than showing an empty table that implies a fault. */}
      {s && !s.configured && (
        <div className="px-4 py-3 rounded-xl bg-gray-50 border border-gray-200 text-gray-600 text-sm mb-6">
          No inference credentials configured. Deterministic checks — declared intent,
          cost forecast, and behavioral baseline — govern every call on their own.
          Set <code className="font-mono text-xs bg-white px-1 rounded border">VIGIL_FEATHERLESS_API_KEY</code> and
          the per-role model IDs to enable AI judgement.
        </div>
      )}

      <div className="grid grid-cols-4 gap-4 mb-6">
        {[
          { l: 'Requests', v: totals.requests },
          { l: 'Fallbacks', v: totals.fallbacks, hi: totals.fallbacks > 0 },
          { l: 'Failures', v: totals.failures, hi: totals.failures > 0 },
          { l: 'Tokens', v: totals.tokens },
        ].map(x => (
          <div key={x.l} className="stat-card text-center">
            <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">{x.l}</p>
            <p className={`text-2xl font-bold mt-1 ${x.hi ? 'text-orange-600' : 'text-gray-900'}`}>{x.v}</p>
          </div>
        ))}
      </div>

      <div className="card overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-100 flex items-center gap-2">
          <Cpu className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Configured Routes</h2>
        </div>

        {rows.length === 0 ? (
          <div className="py-16 text-center">
            <Cpu className="w-8 h-8 text-gray-300 mx-auto" />
            <p className="text-sm text-gray-500 mt-2">No model calls recorded</p>
            <p className="text-xs text-gray-400 mt-1">
              {s?.configured
                ? 'Routes appear here once a call escalates past deterministic checks.'
                : 'Nothing here is inferred — no provider is configured.'}
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Model</th><th>Role</th><th>Requests</th>
                  <th>Avg latency</th><th>Fallbacks</th><th>Tokens</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(m => (
                  <tr key={m.model_id}>
                    <td className="font-mono text-xs">{m.model_id}</td>
                    <td><span className="pill pill-orange">{ROLE_LABEL[m.role] ?? m.role}</span></td>
                    <td className="font-mono text-xs">{m.requests}</td>
                    <td className="font-mono text-xs">{m.avg_latency_ms}ms</td>
                    <td>
                      {m.fallbacks > 0
                        ? <span className="pill pill-red">{m.fallbacks}</span>
                        : <span className="text-gray-400 text-xs">0</span>}
                    </td>
                    <td className="font-mono text-xs">{m.total_tokens}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {s && s.roles.length > 0 && (
        <p className="text-xs text-gray-400 mt-4">
          Configured roles: {s.roles.map(r => ROLE_LABEL[r] ?? r).join(', ')}
        </p>
      )}
    </div>
  )
}
