'use client'

import { useEffect, useRef, useState } from 'react'
import { motion, AnimatePresence } from 'framer-motion'

interface Row {
  id: number
  tool: string
  decision: 'ALLOW' | 'BLOCK'
  reason: string
  live: boolean
}

/**
 * A running verdict feed in the hero.
 *
 * If a Vigil control plane is reachable it shows that server's real decisions.
 * If not it falls back to a scripted loop — and says so, in the label, rather
 * than passing a canned sequence off as live traffic. A governance product
 * that fakes its own telemetry on the marketing page has lost the argument
 * before you reach the docs.
 */

const SCRIPT: Omit<Row, 'id' | 'live'>[] = [
  { tool: 'read_file',       decision: 'ALLOW', reason: 'permitted by declared intent' },
  { tool: 'search_code',     decision: 'ALLOW', reason: 'permitted by declared intent' },
  { tool: 'run_command',     decision: 'BLOCK', reason: 'network access violates declared intent' },
  { tool: 'list_directory',  decision: 'ALLOW', reason: 'permitted by declared intent' },
  { tool: 'read_file',       decision: 'BLOCK', reason: 'access to credentials violates declared intent' },
  { tool: 'analyze_codebase',decision: 'ALLOW', reason: 'permitted by declared intent' },
  { tool: 'read_file',       decision: 'BLOCK', reason: 'Infinite Tool Loop: called 5 times consecutively' },
]

const MAX_ROWS = 4

export function DecisionTicker() {
  const [rows, setRows] = useState<Row[]>([])
  const [live, setLive] = useState(false)
  const idRef = useRef(0)
  const cursor = useRef(0)

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>

    const push = (r: Omit<Row, 'id'>) =>
      setRows(prev => [{ ...r, id: idRef.current++ }, ...prev].slice(0, MAX_ROWS))

    const scripted = () => {
      if (cancelled) return
      const next = SCRIPT[cursor.current % SCRIPT.length]
      cursor.current++
      push({ ...next, live: false })
      timer = setTimeout(scripted, next.decision === 'BLOCK' ? 2600 : 1500)
    }

    const poll = async () => {
      if (cancelled) return
      try {
        const res = await fetch('/api/v1/vigil/decisions?limit=4', { cache: 'no-store' })
        if (!res.ok) throw new Error('unavailable')
        const data = await res.json()
        const ds = data.decisions ?? []
        if (ds.length === 0) throw new Error('empty')

        setLive(true)
        setRows(
          ds.slice().reverse().slice(0, MAX_ROWS).map((d: Record<string, unknown>, i: number) => ({
            id: i,
            tool: String(d.tool ?? ''),
            decision: d.decision === 'ALLOW' ? 'ALLOW' : 'BLOCK',
            reason: String(d.reason ?? ''),
            live: true,
          })),
        )
        timer = setTimeout(poll, 2000)
      } catch {
        // No control plane reachable from the marketing page — expected in
        // most deployments. Fall through to the labelled scripted loop.
        if (cancelled) return
        setLive(false)
        scripted()
      }
    }
    poll()

    return () => { cancelled = true; clearTimeout(timer) }
  }, [])

  return (
    <div className="w-full max-w-[520px]">
      <div className="flex items-center gap-2 mb-3">
        <span className={`w-1.5 h-1.5 rounded-full ${live ? 'bg-[#FF6B00] animate-pulse' : 'bg-white/30'}`} />
        <span className="font-mono text-[10px] tracking-[0.25em] uppercase text-white/40">
          {live ? 'Live decisions' : 'Sample decisions'}
        </span>
      </div>

      <div className="space-y-px">
        <AnimatePresence initial={false} mode="popLayout">
          {rows.map(r => (
            <motion.div
              key={r.id}
              layout
              initial={{ opacity: 0, x: -16 }}
              animate={{ opacity: 1, x: 0 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.45, ease: [0.16, 1, 0.3, 1] }}
              className="flex items-baseline gap-3 py-1.5 border-b border-white/[0.06]"
            >
              <span
                className={`font-mono text-[10px] font-bold tracking-wider w-12 shrink-0 ${
                  r.decision === 'BLOCK' ? 'text-[#FF6B00]' : 'text-white/45'
                }`}
              >
                {r.decision}
              </span>
              <span className="font-mono text-xs text-white/85 w-32 shrink-0 truncate">{r.tool}</span>
              <span className="text-[11px] text-white/35 truncate">{r.reason}</span>
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </div>
  )
}
