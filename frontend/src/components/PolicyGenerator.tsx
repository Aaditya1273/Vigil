'use client'

import { useState } from 'react'
import { Sparkles, AlertTriangle, Check, X } from 'lucide-react'

interface Policy {
  declared_intent: string
  allowed_tools: string[]
  denied_tools: string[]
  allowed_resources: string[]
  denied_resources: string[]
  budget_usd: number
  risk_tolerance: string
  network_access: boolean
  secret_access: boolean
}

interface Draft {
  id: string
  session_id: string
  policy: Policy
  dangerous: string[]
  model_used: string
  provider: string
}

const EXAMPLE =
  'Allow reading project files and running tests. Block network access, secrets, ' +
  'and arbitrary shell commands. Limit this session to $2.'

/**
 * Compiles plain English into a policy, then makes a human approve it.
 *
 * The draft returned by the backend is inert — the model has no path to
 * enforcement. Applying it is a separate, explicit request, which is the whole
 * reason this is two steps instead of one.
 */
export function PolicyGenerator({ sessionId, onApplied }: { sessionId: string; onApplied?: () => void }) {
  const [text, setText] = useState('')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const generate = async () => {
    setBusy(true); setErr(null); setDraft(null)
    try {
      const r = await fetch('/api/v1/vigil/policy/draft', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId, text }),
      })
      const d = await r.json()
      if (!r.ok) { setErr(d.error ?? 'Generation failed'); return }
      setDraft(d.draft)
    } catch {
      setErr('Control plane unreachable')
    } finally {
      setBusy(false)
    }
  }

  const apply = async () => {
    if (!draft) return
    setBusy(true)
    try {
      const r = await fetch(`/api/v1/vigil/policy/draft/${draft.id}/confirm`, { method: 'POST' })
      if (r.ok) { setDraft(null); setText(''); onApplied?.() }
      else setErr('Could not apply the policy')
    } catch {
      setErr('Control plane unreachable')
    } finally {
      setBusy(false)
    }
  }

  const discard = async () => {
    if (!draft) return
    await fetch(`/api/v1/vigil/policy/draft/${draft.id}`, { method: 'DELETE' }).catch(() => {})
    setDraft(null)
  }

  return (
    <div className="card p-6 mb-6">
      <div className="flex items-center gap-2 mb-1">
        <Sparkles className="w-4 h-4 text-orange-600" />
        <h2 className="text-sm font-semibold text-gray-800">Describe a policy in plain English</h2>
      </div>
      <p className="text-xs text-gray-500 mb-4">
        Compiled to a structured policy, validated, then shown to you for approval.
        Nothing is enforced until you apply it.
      </p>

      <textarea
        className="w-full rounded-xl border border-gray-200 p-3 text-sm font-mono h-24 focus:outline-none focus:border-orange-400"
        placeholder={EXAMPLE}
        value={text}
        onChange={e => setText(e.target.value)}
      />

      <div className="flex items-center gap-3 mt-3">
        <button className="btn-orange" onClick={generate} disabled={busy || !text.trim()}>
          {busy ? 'Compiling…' : 'Generate'}
        </button>
        <button className="btn-ghost text-xs" onClick={() => setText(EXAMPLE)} disabled={busy}>
          Use the example
        </button>
      </div>

      {err && (
        <div className="mt-4 px-4 py-3 rounded-xl bg-orange-50 border border-orange-200 text-orange-700 text-sm flex items-start gap-2">
          <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
          <span>{err}</span>
        </div>
      )}

      {draft && (
        <div className="mt-6 border-t border-gray-100 pt-5">
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-semibold text-gray-800">Proposed policy</h3>
            <div className="flex items-center gap-2">
              <span className="pill pill-gray">{draft.provider}</span>
              <span className="pill pill-orange">NOT ENFORCED</span>
            </div>
          </div>

          {draft.dangerous?.length > 0 && (
            <div className="mb-4 px-4 py-3 rounded-xl bg-orange-50 border border-orange-200">
              <p className="text-xs font-semibold text-orange-800 mb-1.5 flex items-center gap-1.5">
                <AlertTriangle className="w-3.5 h-3.5" /> Review before applying
              </p>
              <ul className="text-xs text-orange-700 space-y-1">
                {draft.dangerous.map((d, i) => <li key={i}>• {d}</li>)}
              </ul>
            </div>
          )}

          <pre className="text-xs font-mono bg-gray-50 border border-gray-200 rounded-xl p-4 overflow-x-auto">
            {JSON.stringify(draft.policy, null, 2)}
          </pre>

          <div className="flex items-center gap-3 mt-4">
            <button className="btn-orange flex items-center gap-1.5" onClick={apply} disabled={busy}>
              <Check className="w-3.5 h-3.5" /> Apply this policy
            </button>
            <button className="btn-ghost text-xs flex items-center gap-1.5" onClick={discard} disabled={busy}>
              <X className="w-3.5 h-3.5" /> Discard
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
