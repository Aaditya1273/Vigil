'use client'

import { useEffect, useState } from 'react'
import { PolicyGenerator } from '@/components/PolicyGenerator'
import { Shield, Plus, Check } from 'lucide-react'

interface Policy {
  name: string
  condition: { metric: string; threshold: number; operator: string }
  action: string
}

interface IntentPolicy {
  session_id?: string
  declared_intent?: string
  allowed_tools?: string[]
  denied_tools?: string[]
  budget_usd?: number
  risk_tolerance?: string
  network_access?: boolean
  secret_access?: boolean
  created_at?: string
}

export default function PoliciesPage() {
  const [policies, setPolicies]           = useState<Policy[]>([])
  const [intentPolicies, setIntentPolicies] = useState<IntentPolicy[]>([])
  const [loading, setLoading]             = useState(true)
  const [adding, setAdding]               = useState(false)
  const [form, setForm]                   = useState({ name: '', threshold: '5', action: 'KILL_RUN' })

  const load = async () => {
    try {
      // Cost policies
      const r = await fetch('/api/v1/vigil/cost/policies')
      if (r.ok) setPolicies(await r.json() ?? [])
      // Active session intent policy
      const r2 = await fetch('/api/v1/vigil/sessions/default/policy')
      if (r2.ok) {
        const d = await r2.json()
        if (d?.policy) setIntentPolicies([d.policy])
      }
    } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const addPolicy = async () => {
    if (!form.name) return
    await fetch('/api/v1/vigil/cost/policies', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: form.name,
        condition: { metric: 'total_cost', threshold: parseFloat(form.threshold), operator: 'gt' },
        action: form.action,
      }),
    })
    setAdding(false)
    setForm({ name: '', threshold: '5', action: 'KILL_RUN' })
    load()
  }

  return (
    <div className="p-8 max-w-5xl mx-auto animate-fadeIn">
      <div className="flex items-start justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Policies</h1>
          <p className="text-sm text-gray-500 mt-1">Cost and governance enforcement policies applied to all agents.</p>
        </div>
        <button onClick={() => setAdding(true)} className="btn-orange">
          <Plus className="w-4 h-4" /> New Policy
        </button>
      </div>

      <PolicyGenerator sessionId="default" onApplied={load} />

      {adding && (
        <div className="card p-5 mb-6 space-y-4">
          <h3 className="text-sm font-semibold text-gray-800">New Cost Policy</h3>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Policy Name</label>
              <input value={form.name} onChange={e => setForm(f => ({...f, name: e.target.value}))}
                placeholder="e.g. Block at $10"
                className="w-full border border-gray-200 rounded-xl px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-orange-500/20 focus:border-orange-400" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Cost Threshold ($)</label>
              <input type="number" value={form.threshold} onChange={e => setForm(f => ({...f, threshold: e.target.value}))}
                className="w-full border border-gray-200 rounded-xl px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-orange-500/20 focus:border-orange-400" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Action</label>
              <select value={form.action} onChange={e => setForm(f => ({...f, action: e.target.value}))}
                className="w-full border border-gray-200 rounded-xl px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-orange-500/20">
                {['KILL_RUN','ALERT','CIRCUIT_BREAKER','TRIGGER_FALLBACK'].map(a => <option key={a}>{a}</option>)}
              </select>
            </div>
          </div>
          <div className="flex gap-2">
            <button onClick={addPolicy} className="btn-orange text-sm py-2 px-5">Save Policy</button>
            <button onClick={() => setAdding(false)} className="btn-ghost text-sm py-2 px-4">Cancel</button>
          </div>
        </div>
      )}

      <div className="card overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-100 flex items-center gap-2">
          <Shield className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Active Policies</h2>
        </div>
        {loading ? (
          <div className="py-10 flex justify-center">
            <div className="w-5 h-5 rounded-full border-2 border-orange-500 border-t-transparent animate-spin" />
          </div>
        ) : (intentPolicies.length === 0 && policies.length === 0) ? (
          <div className="py-14 text-center">
            <Shield className="w-8 h-8 text-gray-300 mx-auto mb-3" />
            <p className="text-sm text-gray-500">No policies yet.</p>
            <p className="text-xs text-gray-400 mt-1">Generate a policy above and click <b>Apply this policy</b>.</p>
          </div>
        ) : (
          <div className="divide-y divide-gray-100">
            {/* Intent policies applied via PolicyGenerator */}
            {intentPolicies.map((p, i) => (
              <div key={`intent-${i}`} className="px-6 py-4 flex items-start justify-between gap-4">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="pill pill-green">
                      <span className="pill-dot bg-green-500" />
                      Enforcing
                    </span>
                    <span className="pill pill-gray text-xs">Intent Policy</span>
                  </div>
                  <p className="text-sm font-medium text-gray-800 truncate">{p.declared_intent ?? 'Applied policy'}</p>
                  <div className="flex flex-wrap gap-3 mt-2 text-xs text-gray-500">
                    {p.budget_usd != null && <span>Budget: <b className="text-orange-600">${p.budget_usd}</b></span>}
                    {p.risk_tolerance && <span>Risk: <b>{p.risk_tolerance}</b></span>}
                    {p.network_access === false && <span className="text-red-500">🚫 No network</span>}
                    {p.secret_access === false && <span className="text-red-500">🚫 No secrets</span>}
                    {p.denied_tools && p.denied_tools.length > 0 && <span>Denied tools: <b>{p.denied_tools.join(', ')}</b></span>}
                  </div>
                </div>
                <Check className="w-4 h-4 text-green-500 shrink-0 mt-1" />
              </div>
            ))}
            {/* Cost policies */}
            {policies.length > 0 && (
              <table className="data-table">
                <thead><tr><th>Name</th><th>Metric</th><th>Threshold</th><th>Operator</th><th>Action</th></tr></thead>
                <tbody>
                  {policies.map((p, i) => (
                    <tr key={i}>
                      <td className="font-medium text-gray-800">{p.name}</td>
                      <td className="font-mono text-xs text-gray-500">{p.condition?.metric}</td>
                      <td className="font-mono text-xs text-orange-600">${p.condition?.threshold}</td>
                      <td className="text-xs text-gray-500">{p.condition?.operator}</td>
                      <td><span className="pill pill-orange">{p.action}</span></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
