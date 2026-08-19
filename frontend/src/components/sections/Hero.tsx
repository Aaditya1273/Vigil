'use client'

import { useEffect, useRef, useState } from 'react'
import { gsap, ScrollTrigger } from '@/lib/gsap'
import dynamic from 'next/dynamic'
import { DecisionTicker } from '../hero/DecisionTicker'
import { Magnetic } from '../motion/Reveal'

/**
 * Three.js is ~800 KB and cannot server-render anyway, so it is split out and
 * streamed in behind the curtain rather than blocking first paint. The
 * curtain's 1.1s floor is deliberately long enough for it to arrive, which is
 * what the preloader is actually buying — not just theatre.
 */
const Gate = dynamic(() => import('../hero/Gate').then(m => m.Gate), {
  ssr: false,
  loading: () => null,
})

/**
 * Hero. Pinned for three viewport heights while the scroll tightens the
 * firewall and pushes the camera through it.
 *
 * The background is not decoration: `blockRate` is a real control, and scroll
 * drives it from 12% to 86%. By the time the section releases you have watched
 * the product go from permissive to locked down, and the counter has told you
 * the number the whole way.
 */
export function Hero({ ready }: { ready: boolean }) {
  const root = useRef<HTMLDivElement>(null)
  const headline = useRef<HTMLHeadingElement>(null)
  const [blockRate, setBlockRate] = useState(0.12)
  const [scroll, setScroll] = useState(0)

  // Intro: only once the curtain is gone, so the reveal is not spent behind it.
  useEffect(() => {
    if (!ready) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      gsap.set('[data-hero-in]', { opacity: 1, yPercent: 0 })
      return
    }
    const ctx = gsap.context(() => {
      gsap
        .timeline({ defaults: { ease: 'expo.out' } })
        .to('[data-hero-in]', { opacity: 1, y: 0, duration: 1.0, stagger: 0.08 }, 0.35)
    }, root)
    return () => ctx.revert()
  }, [ready])

  // Scroll: pin, then scrub the firewall tightening.
  useEffect(() => {
    const el = root.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const ctx = gsap.context(() => {
      ScrollTrigger.create({
        trigger: el,
        start: 'top top',
        end: '+=300%',
        pin: '[data-hero-stage]',
        pinSpacing: true,
        scrub: true,
        onUpdate: self => {
          const p = self.progress
          setBlockRate(0.12 + p * 0.74)
          setScroll(p)
        },
      })

      // Type lifts and dims as the camera travels, so the 3D takes over.
      gsap.to('[data-hero-copy]', {
        yPercent: -60,
        opacity: 0,
        ease: 'none',
        scrollTrigger: { trigger: el, start: 'top top', end: '+=180%', scrub: true },
      })
    }, el)

    return () => ctx.revert()
  }, [])

  return (
    <section ref={root} className="relative" style={{ background: '#080808' }}>
      <div data-hero-stage className="relative h-screen w-full overflow-hidden">
        <Gate
          blockRate={blockRate}
          scroll={scroll}
          intensity={ready ? 1 : 0}
          className="absolute inset-0"
        />

        {/* Legibility floor. Kept asymmetric so the field still breathes on the right. */}
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#080808] via-[#080808]/35 to-transparent" />
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-r from-[#080808] via-[#080808]/30 to-transparent" />

        {/* Rail */}
        <div className="absolute inset-x-0 top-0 z-30 flex items-center justify-between px-[5vw] py-7">
          <span data-hero-in className="font-[900] text-[#FDFCF8] text-lg tracking-[-0.04em]" style={{ opacity: 0 }}>
            VIGIL
          </span>
          <nav className="hidden items-center gap-9 md:flex">
            {['Product', 'Governance', 'Security', 'Docs'].map(l => (
              <a
                key={l}
                data-hero-in
                href="#"
                className="font-mono text-[10px] uppercase tracking-[0.22em] text-white/40 transition-colors hover:text-[#FF6B00]"
                style={{ opacity: 0 }}
              >
                {l}
              </a>
            ))}
          </nav>
          <Magnetic>
            <a
              data-hero-in
              href="#"
              className="rounded-full border border-white/15 px-5 py-2 font-mono text-[10px] uppercase tracking-[0.22em] text-white/70 transition-colors hover:border-[#FF6B00] hover:text-[#FF6B00]"
              style={{ opacity: 0 }}
            >
              Deploy
            </a>
          </Magnetic>
        </div>

        {/* Copy */}
        <div data-hero-copy className="absolute inset-x-0 bottom-0 z-20 px-[5vw] pb-[8vh]">
          <div className="mx-auto max-w-[1680px]">
            <h1
              ref={headline}
              className="font-[900] leading-[0.79] tracking-[-0.06em] text-[#FDFCF8]
                         text-[14vw] md:text-[11vw] lg:text-[8.6vw]"
            >
              {/* The headline reveal is CSS, not GSAP. It has to be the one
                  thing on the page that cannot fail: it lives inside a pinned
                  ScrollTrigger, and a tween competing with the pin's own style
                  management left the type stuck off-screen. A transition on a
                  class has no such lifecycle to lose to. */}
              {['EVERY CALL', 'JUDGED BEFORE', 'IT RUNS.'].map((line, i) => (
                <span key={i} className="block overflow-hidden">
                  <span
                    data-hero-line
                    className="block will-change-transform"
                    style={{
                      transform: ready ? 'translateY(0%)' : 'translateY(115%)',
                      transition: 'transform 1.3s cubic-bezier(.16,1,.3,1)',
                      transitionDelay: `${0.1 + i * 0.09}s`,
                    }}
                  >
                    {i === 1 ? (
                      <>
                        <span className="text-[#FF6B00]">JUDGED</span> BEFORE
                      </>
                    ) : (
                      line
                    )}
                  </span>
                </span>
              ))}
            </h1>

            <div className="mt-10 flex flex-col items-start justify-between gap-10 lg:flex-row lg:items-end">
              <p data-hero-in className="max-w-[420px] text-sm leading-relaxed text-white/50 md:text-base" style={{ opacity: 0 }}>
                Vigil stands between an autonomous agent and its tools. Declared intent,
                live cost, and behavioural baseline are checked on every call —
                before execution, not after the damage.
              </p>
              <div data-hero-in className="lg:mr-[16vw]" style={{ opacity: 0 }}>
                <DecisionTicker />
              </div>
            </div>
          </div>
        </div>

        {/* Live readout — proof the scroll is doing something measurable */}
        <div className="absolute bottom-[8vh] right-[5vw] z-30 hidden text-right lg:block">
          <p className="mb-2 font-mono text-[9px] uppercase tracking-[0.3em] text-white/25">Refused</p>
          <p className="font-[900] leading-none tabular-nums text-[#FF6B00] text-5xl">
            {Math.round(blockRate * 100)}
            <span className="text-2xl">%</span>
          </p>
          <div className="mt-3 ml-auto h-16 w-px bg-white/10">
            <div
              className="w-full bg-[#FF6B00] transition-[height] duration-200"
              style={{ height: `${blockRate * 100}%` }}
            />
          </div>
        </div>

        {/* Scroll cue */}
        <div
          data-hero-in
          className="absolute bottom-6 left-1/2 z-30 flex -translate-x-1/2 flex-col items-center gap-2"
          style={{ opacity: 0 }}
        >
          <span className="font-mono text-[9px] uppercase tracking-[0.3em] text-white/25">
            Scroll to tighten
          </span>
          <span className="h-10 w-px overflow-hidden bg-white/10">
            <span className="block h-1/2 w-full animate-[drop_2.2s_ease-in-out_infinite] bg-[#FF6B00]" />
          </span>
        </div>
      </div>
    </section>
  )
}
