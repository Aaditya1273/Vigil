'use client'

import { useEffect, useRef, useState } from 'react'
import { gsap } from '@/lib/gsap'

/**
 * Cold open. Counter, wordmark build, then the floor drops out.
 *
 * The count tracks `document.readyState`, not a timer, so it cannot sit at 98%
 * on an already-interactive page or flash past before anything has loaded. It
 * is a beat that happens to be true.
 *
 * The exit is a clip-path wipe rather than a fade: the page underneath is
 * revealed, not cross-dissolved into, which is what makes it feel like a
 * curtain instead of a loading spinner.
 */
export function Curtain({ onDone }: { onDone: () => void }) {
  const root = useRef<HTMLDivElement>(null)
  const num = useRef<HTMLSpanElement>(null)
  const bar = useRef<HTMLDivElement>(null)
  const word = useRef<HTMLDivElement>(null)
  const [pct, setPct] = useState(0)

  useEffect(() => {
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const ctx = gsap.context(() => {
      const counter = { v: 0 }
      let raf = 0
      const start = performance.now()
      const MIN = reduced ? 0 : 1100
      const MAX = reduced ? 0 : 3000

      // Letters of the wordmark rise in sequence while the count runs.
      if (!reduced && word.current) {
        gsap.fromTo(
          word.current.querySelectorAll('span'),
          { yPercent: 120, opacity: 0 },
          { yPercent: 0, opacity: 1, duration: 0.9, stagger: 0.045, ease: 'power3.out', delay: 0.1 },
        )
      }

      const tick = (t: number) => {
        const elapsed = t - start
        const ready = document.readyState === 'complete'
        const byTime = Math.min(1, elapsed / Math.max(MIN, 1))
        const target = ready ? byTime : Math.min(byTime, 0.9)

        counter.v = Math.max(counter.v, target)
        setPct(Math.round(counter.v * 100))
        if (bar.current) bar.current.style.transform = `scaleX(${counter.v})`

        if ((ready && elapsed >= MIN) || elapsed >= MAX) {
          setPct(100)
          if (bar.current) bar.current.style.transform = 'scaleX(1)'
          exit()
          return
        }
        raf = requestAnimationFrame(tick)
      }

      const exit = () => {
        const tl = gsap.timeline({ onComplete: onDone })
        if (reduced) {
          tl.set(root.current, { autoAlpha: 0 })
          return
        }
        tl.to([num.current, word.current, bar.current], {
          autoAlpha: 0, duration: 0.4, ease: 'power2.in',
        })
          .to(root.current, {
            // Wipe upward from the bottom edge.
            clipPath: 'inset(0% 0% 100% 0%)',
            duration: 1.15,
            ease: 'expo.inOut',
          }, '-=0.1')
          .set(root.current, { display: 'none' })
      }

      raf = requestAnimationFrame(tick)
      return () => cancelAnimationFrame(raf)
    }, root)

    return () => ctx.revert()
  }, [onDone])

  return (
    <div
      ref={root}
      className="fixed inset-0 z-[10000] flex flex-col justify-between px-[5vw] py-[5vh]"
      style={{ background: '#080808', clipPath: 'inset(0% 0% 0% 0%)' }}
    >
      <div className="flex items-start justify-between">
        <div ref={word} className="flex overflow-hidden">
          {'VIGIL'.split('').map((c, i) => (
            <span key={i} className="block font-[900] text-[#FDFCF8] text-2xl tracking-[-0.04em]">
              {c}
            </span>
          ))}
        </div>
        <span className="font-mono text-[10px] tracking-[0.3em] uppercase text-white/30">
          Runtime firewall
        </span>
      </div>

      <div className="flex items-end justify-between">
        <span className="font-mono text-[10px] tracking-[0.3em] uppercase text-white/30 mb-4">
          Establishing control plane
        </span>
        <span
          ref={num}
          className="font-[900] leading-[0.8] tabular-nums text-[22vw] md:text-[14vw]"
          style={{ color: pct >= 100 ? '#FF6B00' : '#FDFCF8', transition: 'color .4s ease' }}
        >
          {String(pct).padStart(3, '0')}
        </span>
      </div>

      <div
        ref={bar}
        className="absolute bottom-0 left-0 h-px w-full origin-left bg-[#FF6B00]"
        style={{ transform: 'scaleX(0)' }}
      />
    </div>
  )
}
