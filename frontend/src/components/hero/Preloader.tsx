'use client'

import { useEffect, useState } from 'react'
import { motion, AnimatePresence } from 'framer-motion'

/**
 * Cold-open curtain with a counter, then a split reveal.
 *
 * The counter tracks a real signal — document readiness — rather than a fixed
 * timer, so it cannot sit at 98% while the page is already interactive, or
 * flash past before anything has loaded. It is a beat, not a lie.
 */
export function Preloader({ onDone }: { onDone: () => void }) {
  const [pct, setPct] = useState(0)
  const [gone, setGone] = useState(false)

  useEffect(() => {
    // Reduced motion collapses the hold to zero rather than short-circuiting
    // with a synchronous setState. Same outcome, but every state write stays
    // inside the rAF callback instead of the effect body, and there is no
    // client-only initial state to desync from the server render.
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

    let raf = 0
    const start = performance.now()
    const MIN = reduced ? 0 : 900   // long enough to register as intent, short enough not to annoy
    const MAX = reduced ? 0 : 2600  // hard ceiling — never hold the page hostage to a slow asset

    const tick = (t: number) => {
      const elapsed = t - start
      const ready = document.readyState === 'complete'
      // Progress is the slower of "time served" and "actually loaded", so the
      // bar reflects both without either stalling it.
      const byTime = Math.min(1, elapsed / MIN)
      const target = ready ? byTime : Math.min(byTime, 0.92)
      const next = Math.round(target * 100)

      setPct(p => (next > p ? next : p))

      if ((ready && elapsed >= MIN) || elapsed >= MAX) {
        setPct(100)
        setTimeout(() => { setGone(true); onDone() }, reduced ? 0 : 420)
        return
      }
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [onDone])

  return (
    <AnimatePresence>
      {!gone && (
        <motion.div
          className="fixed inset-0 z-[100] flex items-end justify-between px-[5vw] pb-[5vh] pointer-events-none"
          style={{ background: '#0A0A0A' }}
          exit={{ y: '-100%' }}
          transition={{ duration: 1.0, ease: [0.76, 0, 0.24, 1] }}
        >
          <motion.span
            className="font-mono text-white/40 text-xs tracking-[0.3em] uppercase"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.15 }}
          >
            Vigil — runtime firewall
          </motion.span>

          <span
            className="font-[900] leading-none tabular-nums text-[18vw] md:text-[12vw]"
            style={{ color: pct >= 100 ? '#FF6B00' : '#FDFCF8', transition: 'color .35s ease' }}
          >
            {String(pct).padStart(3, '0')}
          </span>

          {/* Fill bar doubles as the progress readout for anyone not watching the number. */}
          <motion.div
            className="absolute bottom-0 left-0 h-[2px]"
            style={{ background: '#FF6B00', width: `${pct}%` }}
          />
        </motion.div>
      )}
    </AnimatePresence>
  )
}
