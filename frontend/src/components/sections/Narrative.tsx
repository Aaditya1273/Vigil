'use client'

import { useEffect, useRef, useState } from 'react'
import { gsap, ScrollTrigger } from '@/lib/gsap'

/**
 * The argument, told by scrolling.
 *
 * Four beats, pinned. Scroll scrubs between them: the numeral counts, the
 * headline swaps, and a diagram redraws itself. This is the section that has to
 * earn the scroll — a static feature grid here would undo the hero.
 *
 * States are indexed rather than cross-faded en masse, so only one beat is ever
 * animating and the pin never composites four full layers at once.
 */

const BEATS = [
  {
    n: '01',
    kicker: 'The failure',
    title: 'It does exactly what you asked.',
    body: 'An agent does not break. It works precisely as designed while doing something nobody sanctioned — a loop that burns a budget, a read that touches a credential, a command that reaches the network.',
  },
  {
    n: '02',
    kicker: 'Why guardrails fail',
    title: 'Advice is not enforcement.',
    body: 'Prompt-level rules can be argued out of. Static allowlists are context-free — run_command is either on or off, with no notion of this command in this session. Observability tells you afterwards, when the spend is already spent.',
  },
  {
    n: '03',
    kicker: 'The layer',
    title: 'Decide before execution.',
    body: 'Vigil sits on the hot path. Declared intent, cost forecast, and behavioural baseline resolve on every call — cheap, explainable, reproducible, and finished before the tool is ever reached.',
  },
  {
    n: '04',
    kicker: 'The escalation',
    title: 'A model, only when uncertain.',
    body: 'Deterministic checks answer the common case for free. A language model is consulted only when they genuinely cannot decide — and it may tighten a verdict, never relax one.',
  },
]

export function Narrative() {
  const root = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(0)
  const [progress, setProgress] = useState(0)

  useEffect(() => {
    const el = root.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const ctx = gsap.context(() => {
      ScrollTrigger.create({
        trigger: el,
        start: 'top top',
        end: `+=${BEATS.length * 100}%`,
        pin: '[data-stage]',
        scrub: true,
        onUpdate: self => {
          setProgress(self.progress)
          // Clamp: at progress exactly 1 the raw index overflows the array.
          setActive(Math.min(BEATS.length - 1, Math.floor(self.progress * BEATS.length)))
        },
      })
    }, el)

    return () => ctx.revert()
  }, [])

  return (
    <section ref={root} className="relative" style={{ background: '#080808' }}>
      <div data-stage className="relative h-screen w-full overflow-hidden">
        {/* Rule that fills across the beats */}
        <div className="absolute left-0 top-0 h-px w-full bg-white/[0.07]">
          <div
            className="h-full bg-[#FF6B00] transition-[width] duration-150 ease-out"
            style={{ width: `${progress * 100}%` }}
          />
        </div>

        <div className="mx-auto grid h-full max-w-[1680px] grid-cols-1 items-center gap-12 px-[5vw] lg:grid-cols-[1fr_0.9fr]">
          {/* Text column */}
          <div className="relative">
            <div className="mb-10 flex items-baseline gap-5">
              <span className="font-[900] tabular-nums leading-none text-[#FF6B00] text-[16vw] lg:text-[9vw]">
                {BEATS[active].n}
              </span>
              <span className="font-mono text-[10px] uppercase tracking-[0.3em] text-white/30">
                {BEATS[active].kicker}
              </span>
            </div>

            {/* keyed on `active` so React remounts and the entry animation replays */}
            <div key={active} className="animate-[beatIn_.7s_cubic-bezier(.16,1,.3,1)_both]">
              <h2 className="max-w-[16ch] font-[900] leading-[0.92] tracking-[-0.04em] text-[#FDFCF8] text-[8vw] lg:text-[3.6vw]">
                {BEATS[active].title}
              </h2>
              <p className="mt-7 max-w-[46ch] text-sm leading-relaxed text-white/45 md:text-base">
                {BEATS[active].body}
              </p>
            </div>

            <div className="mt-12 flex gap-2">
              {BEATS.map((_, i) => (
                <span
                  key={i}
                  className={`h-px transition-all duration-500 ${
                    i === active ? 'w-12 bg-[#FF6B00]' : 'w-5 bg-white/15'
                  }`}
                />
              ))}
            </div>
          </div>

          {/* Diagram column — redraws per beat */}
          <div className="relative hidden h-[60vh] items-center justify-center lg:flex">
            <Diagram beat={active} />
          </div>
        </div>
      </div>
    </section>
  )
}

/**
 * A schematic that answers the beat beside it. Hand-drawn SVG rather than an
 * illustration: crisp at any size, costs nothing to ship, and can animate its
 * own geometry.
 */
function Diagram({ beat }: { beat: number }) {
  const mono = { font: '9px monospace', letterSpacing: '.2em' } as const

  return (
    <svg viewBox="0 0 400 400" className="h-full w-full max-w-[460px]" fill="none">
      <circle cx="200" cy="200" r="150" stroke="rgba(255,255,255,0.05)" />
      <circle cx="200" cy="200" r="110" stroke="rgba(255,255,255,0.04)" />

      <g opacity={0.85}>
        <circle cx="60" cy="200" r="5" fill="#8FA3BF" />
        <text x="60" y="228" textAnchor="middle" fill="rgba(255,255,255,0.3)" style={mono}>AGENT</text>
      </g>
      <g opacity={0.85}>
        <circle cx="340" cy="200" r="5" fill="#8FA3BF" />
        <text x="340" y="228" textAnchor="middle" fill="rgba(255,255,255,0.3)" style={mono}>TOOL</text>
      </g>

      {/* Beats 1–2: nothing in the way */}
      {beat < 2 && (
        <g>
          <line x1="65" y1="200" x2="335" y2="200" stroke="#8FA3BF" strokeWidth="1" opacity="0.45" />
          <circle r="4" fill="#8FA3BF">
            <animateMotion dur="2.4s" repeatCount="indefinite" path="M65,200 L335,200" />
          </circle>
          {beat === 1 && (
            <text x="200" y="180" textAnchor="middle" fill="rgba(255,255,255,0.25)" style={mono}>NO CHECK</text>
          )}
        </g>
      )}

      {/* Beats 3–4: the barrier stands in the path */}
      {beat >= 2 && (
        <g>
          <line x1="65" y1="200" x2="195" y2="200" stroke="#8FA3BF" strokeWidth="1" opacity="0.5" />
          <line x1="205" y1="200" x2="335" y2="200" stroke="#8FA3BF" strokeWidth="1" opacity="0.22" strokeDasharray="3 4" />

          <line x1="200" y1="70" x2="200" y2="330" stroke="#FF6B00" strokeWidth="1.5" opacity="0.75">
            <animate attributeName="opacity" values="0.45;0.9;0.45" dur="3s" repeatCount="indefinite" />
          </line>

          <circle r="4" fill="#8FA3BF">
            <animateMotion dur="2.8s" repeatCount="indefinite" path="M65,200 L335,200" />
          </circle>
          <circle r="4" fill="#FF6B00">
            <animateMotion dur="1.9s" repeatCount="indefinite" path="M65,170 L200,170" />
          </circle>
          <circle cx="200" cy="170" r="0" fill="none" stroke="#FF6B00">
            <animate attributeName="r" values="0;22" dur="1.9s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0.85;0" dur="1.9s" repeatCount="indefinite" />
          </circle>

          <text x="200" y="56" textAnchor="middle" fill="#FF6B00" style={mono}>VIGIL</text>

          {beat === 3 && (
            <g>
              <line x1="200" y1="200" x2="290" y2="118" stroke="#FF6B00" strokeWidth="1" opacity="0.35" strokeDasharray="2 4" />
              <circle cx="290" cy="118" r="4" fill="#FF6B00" opacity="0.8" />
              <text x="298" y="112" fill="rgba(255,255,255,0.35)" style={{ font: '9px monospace', letterSpacing: '.15em' }}>JUDGE</text>
              <text x="298" y="126" fill="rgba(255,255,255,0.2)" style={{ font: '8px monospace' }}>only if uncertain</text>
            </g>
          )}
        </g>
      )}
    </svg>
  )
}
