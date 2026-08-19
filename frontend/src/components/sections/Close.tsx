'use client'

import { useEffect, useRef, useState } from 'react'
import { gsap } from '@/lib/gsap'
import { SplitReveal, FadeUp, Magnetic } from '../motion/Reveal'

/** Counts up when it enters view. Numbers that animate read as measured. */
function Stat({ value, suffix = '', label }: { value: number; suffix?: string; label: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  const [shown, setShown] = useState(0)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    // Reduced motion collapses the count to an instant set rather than
    // short-circuiting with a synchronous setState: the write stays inside
    // gsap's onUpdate callback either way.
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

    const ctx = gsap.context(() => {
      const o = { v: 0 }
      gsap.to(o, {
        v: value,
        duration: reduced ? 0 : 1.8,
        ease: 'expo.out',
        onUpdate: () => setShown(Math.round(o.v)),
        scrollTrigger: { trigger: el, start: 'top 88%', once: true },
      })
    }, el)
    return () => ctx.revert()
  }, [value])

  return (
    <div>
      <span ref={ref} className="block font-[900] tabular-nums leading-none text-[#FDFCF8] text-[9vw] lg:text-[3.4vw]">
        {shown}
        <span className="text-[#FF6B00]">{suffix}</span>
      </span>
      <span className="mt-3 block font-mono text-[10px] uppercase tracking-[0.25em] text-white/30">
        {label}
      </span>
    </div>
  )
}

export function Close() {
  const root = useRef<HTMLElement>(null)

  useEffect(() => {
    const el = root.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const ctx = gsap.context(() => {
      // The seam widens as the section arrives — a last echo of the barrier.
      gsap.fromTo(
        '[data-seam]',
        { scaleY: 0 },
        {
          scaleY: 1,
          ease: 'none',
          scrollTrigger: { trigger: el, start: 'top 80%', end: 'center center', scrub: 0.8 },
        },
      )
    }, el)
    return () => ctx.revert()
  }, [])

  return (
    <section ref={root} className="relative overflow-hidden py-[18vh]" style={{ background: '#080808' }}>
      {/* Vertical seam, echoing the hero's plane */}
      <div
        data-seam
        className="pointer-events-none absolute left-1/2 top-0 h-full w-px origin-top
                   bg-gradient-to-b from-transparent via-[#FF6B00]/35 to-transparent"
      />

      <div className="relative z-10 mx-auto max-w-[1680px] px-[5vw]">
        <div className="grid grid-cols-2 gap-10 border-b border-white/[0.07] pb-16 lg:grid-cols-4">
          {/* Every figure here is measured, not marketing. */}
          <Stat value={106} label="Tests passing" />
          <Stat value={6} label="Detectors on the hot path" />
          <Stat value={0} label="Calls billed but not run" />
          <Stat value={100} suffix="%" label="Decisions audited" />
        </div>

        <div className="mt-24 text-center">
          <FadeUp>
            <span className="font-mono text-[10px] uppercase tracking-[0.3em] text-[#FF6B00]">
              Deploy in minutes
            </span>
          </FadeUp>

          <SplitReveal
            as="h2"
            text="Put a firewall in front of it."
            className="mx-auto mt-8 block max-w-[14ch] font-[900] leading-[0.88] tracking-[-0.05em]
                       text-[#FDFCF8] text-[13vw] lg:text-[6.5vw]"
            stagger={0.055}
          />

          <FadeUp delay={0.15}>
            <div className="mt-14 flex flex-col items-center justify-center gap-5 sm:flex-row">
              <Magnetic strength={0.4}>
                <a
                  href="#"
                  className="group inline-flex items-center gap-3 rounded-full bg-[#FF6B00] px-9 py-4
                             font-mono text-[11px] uppercase tracking-[0.2em] text-black
                             transition-colors hover:bg-[#FDFCF8]"
                >
                  Start governing
                  <span className="transition-transform duration-500 group-hover:translate-x-1">→</span>
                </a>
              </Magnetic>

              <Magnetic strength={0.25}>
                <a
                  href="#"
                  className="inline-flex items-center gap-3 rounded-full border border-white/15 px-9 py-4
                             font-mono text-[11px] uppercase tracking-[0.2em] text-white/70
                             transition-colors hover:border-[#FF6B00] hover:text-[#FF6B00]"
                >
                  Read the docs
                </a>
              </Magnetic>
            </div>
          </FadeUp>

          <FadeUp delay={0.25}>
            <p className="mt-10 font-mono text-[10px] tracking-[0.15em] text-white/25">
              claude mcp add --transport http vigil http://localhost:8080/api/v1/mcp
            </p>
          </FadeUp>
        </div>
      </div>
    </section>
  )
}
