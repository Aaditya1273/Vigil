'use client'

import { useEffect, useRef } from 'react'
import { gsap } from '@/lib/gsap'

/**
 * Horizontal gallery: vertical scroll drives the track sideways while the
 * section is pinned.
 *
 * The distance is computed from the track's real width rather than a guessed
 * viewport multiple, and recomputed on resize — hardcoding it is why most
 * horizontal sections stop a card short or run out of content early.
 */

const CARDS = [
  {
    n: '01',
    title: 'Declared intent',
    body: 'A session states what it is for. Every call is judged against that sentence, and the verdict is a sentence too — not an error code.',
    detail: 'run_command · curl → BLOCK\nnetwork access violates declared intent',
  },
  {
    n: '02',
    title: 'Predictive cost',
    body: 'Burn rate over a rolling window, projected total, time to breach. A straight-line projection you can reproduce by hand — not a model.',
    detail: 'burn $0.55/min · projected $2.78\nbreach in ~3m 35s',
  },
  {
    n: '03',
    title: 'Behavioural baseline',
    body: 'Loop, retry storm, latency spike, stuck agent, tool timeout. Deterministic detectors on the hot path, evaluated against the call being requested.',
    detail: 'Infinite Tool Loop → CRITICAL\ncalled 5 times consecutively',
  },
  {
    n: '04',
    title: 'AI security judge',
    body: 'Consulted only when deterministic checks cannot decide. Schema, enum, and range validated — it may tighten a verdict, never relax one.',
    detail: 'risk 91/100 · CRITICAL\nintent_violation: true',
  },
  {
    n: '05',
    title: 'Tamper-evident audit',
    body: 'Every decision sealed into a SHA-256 chain, allows included. Editing, deleting, or reordering any record breaks the link at that point.',
    detail: 'vigil audit verify\nPASS — 187 events verified',
  },
]

export function Capabilities() {
  const root = useRef<HTMLDivElement>(null)
  const track = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = root.current
    const tr = track.current
    if (!el || !tr) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    // Below lg the track is a normal vertical stack; pinning it would trap the
    // user in a section they cannot scroll past on a phone.
    if (!window.matchMedia('(min-width: 1024px)').matches) return

    const ctx = gsap.context(() => {
      // Measured, not guessed: distance is exactly the overflow.
      const distance = () => Math.max(0, tr.scrollWidth - window.innerWidth)

      gsap.to(tr, {
        x: () => -distance(),
        ease: 'none',
        scrollTrigger: {
          trigger: el,
          start: 'top top',
          end: () => `+=${distance()}`,
          pin: true,
          scrub: 1,          // slight lag reads as inertia rather than a slider
          invalidateOnRefresh: true,
          anticipatePin: 1,  // avoids the one-frame jump as the pin engages
        },
      })
    }, el)

    return () => ctx.revert()
  }, [])

  return (
    <section ref={root} className="relative overflow-hidden" style={{ background: '#080808' }}>
      <div className="flex h-screen items-center">
        <div ref={track} className="flex flex-col gap-8 px-[5vw] lg:flex-row lg:gap-10 lg:will-change-transform">
          {/* Lead-in panel, so the track opens with a statement not a card */}
          <div className="flex w-[80vw] shrink-0 flex-col justify-center lg:w-[38vw]">
            <span className="font-mono text-[10px] uppercase tracking-[0.3em] text-[#FF6B00]">
              What runs on every call
            </span>
            <h2 className="mt-6 font-[900] leading-[0.9] tracking-[-0.04em] text-[#FDFCF8] text-[10vw] lg:text-[4.2vw]">
              Five checks.<br />One verdict.
            </h2>
            <p className="mt-6 max-w-[38ch] text-sm leading-relaxed text-white/40">
              Four of them are deterministic and finish in microseconds. The fifth
              only wakes up when the others disagree.
            </p>
          </div>

          {CARDS.map(c => (
            <article
              key={c.n}
              className="group relative flex w-[80vw] shrink-0 flex-col justify-between rounded-2xl border border-white/[0.07]
                         bg-gradient-to-b from-white/[0.035] to-transparent p-8 transition-colors duration-500
                         hover:border-[#FF6B00]/40 lg:h-[58vh] lg:w-[30vw]"
            >
              <div>
                <span className="font-mono text-[10px] tracking-[0.3em] text-white/25">{c.n}</span>
                <h3 className="mt-6 font-[800] leading-tight tracking-[-0.02em] text-[#FDFCF8] text-2xl lg:text-[1.9vw]">
                  {c.title}
                </h3>
                <p className="mt-4 text-sm leading-relaxed text-white/40">{c.body}</p>
              </div>

              <pre className="mt-8 whitespace-pre-wrap rounded-lg border border-white/[0.06] bg-black/40 p-4
                              font-mono text-[10px] leading-relaxed text-[#FF6B00]/70">
                {c.detail}
              </pre>

              {/* Hairline that draws in on hover — the only hover flourish, used
                  consistently so it reads as a system rather than an effect. */}
              <span className="pointer-events-none absolute inset-x-8 bottom-0 h-px origin-left scale-x-0
                               bg-gradient-to-r from-[#FF6B00] to-transparent transition-transform
                               duration-700 group-hover:scale-x-100" />
            </article>
          ))}
        </div>
      </div>
    </section>
  )
}
