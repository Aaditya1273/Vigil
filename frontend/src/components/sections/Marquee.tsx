'use client'

import { useEffect, useRef } from 'react'
import { gsap } from '@/lib/gsap'
import { useSmooth } from '@/lib/smooth'

/**
 * Velocity-reactive marquee.
 *
 * It drifts on its own, but scrolling *skews and accelerates* it in the
 * direction of travel. That coupling is the whole point: a marquee that ignores
 * the scroll is wallpaper, one that responds to it makes the page feel like a
 * single physical object.
 *
 * Implemented with a manual x accumulator wrapped modulo one copy's width,
 * rather than a CSS keyframe, because velocity has to feed in continuously.
 */
export function Marquee({
  items,
  className = '',
}: {
  items: string[]
  className?: string
}) {
  const track = useRef<HTMLDivElement>(null)
  const { velocity } = useSmooth()

  useEffect(() => {
    const el = track.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    // Two identical copies; wrapping at one copy's width makes the seam
    // invisible without measuring individual items.
    const half = el.scrollWidth / 2
    let x = 0
    let skew = 0
    let raf = 0

    const tick = () => {
      const v = velocity.current ?? 0
      // Base drift plus a scroll-proportional boost.
      x -= 0.6 + Math.abs(v) * 0.35
      if (x <= -half) x += half

      const targetSkew = gsap.utils.clamp(-12, 12, v * 0.6)
      skew += (targetSkew - skew) * 0.08

      el.style.transform = `translate3d(${x}px,0,0) skewX(${skew}deg)`
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [velocity])

  const row = [...items, ...items]

  return (
    <div className={`relative overflow-hidden border-y border-white/[0.07] py-8 ${className}`}>
      <div ref={track} className="flex w-max items-center gap-16 will-change-transform">
        {row.map((t, i) => (
          <span key={i} className="flex shrink-0 items-center gap-16">
            <span className="whitespace-nowrap font-[900] uppercase tracking-[-0.03em] text-[#FDFCF8]/85 text-[5vw] lg:text-[3vw]">
              {t}
            </span>
            <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#FF6B00]" />
          </span>
        ))}
      </div>

      {/* Feathered edges so items enter and leave rather than being cut off */}
      <div className="pointer-events-none absolute inset-y-0 left-0 w-[18vw] bg-gradient-to-r from-[#080808] to-transparent" />
      <div className="pointer-events-none absolute inset-y-0 right-0 w-[18vw] bg-gradient-to-l from-[#080808] to-transparent" />
    </div>
  )
}
