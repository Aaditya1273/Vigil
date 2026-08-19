'use client'

import { useEffect, useRef } from 'react'
import { gsap, ScrollTrigger } from '@/lib/gsap'

/**
 * The page's shared motion vocabulary.
 *
 * One easing curve and one set of durations everywhere. Award-looking sites are
 * rarely doing anything exotic per element — they are doing the *same* thing
 * consistently, so the whole page feels authored by one hand.
 */

export const EASE = 'expo.out'
export const DUR = 1.1

/** Words rise out of their own clip, staggered. The workhorse headline reveal. */
export function SplitReveal({
  text,
  as: Tag = 'span',
  className = '',
  delay = 0,
  stagger = 0.06,
  trigger = true,
  play,
}: {
  text: string
  as?: React.ElementType
  className?: string
  delay?: number
  stagger?: number
  /** Reveal on scroll into view. Set false to control with `play`. */
  trigger?: boolean
  /** Manual gate, for content that must wait on the curtain. */
  play?: boolean
}) {
  const root = useRef<HTMLElement>(null)

  useEffect(() => {
    const el = root.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const words = el.querySelectorAll('[data-word]')
    const ctx = gsap.context(() => {
      const anim = {
        yPercent: 0,
        duration: DUR,
        ease: EASE,
        stagger,
        delay,
      }
      gsap.set(words, { yPercent: 115 })

      if (trigger) {
        gsap.to(words, {
          ...anim,
          scrollTrigger: { trigger: el, start: 'top 88%', once: true },
        })
      } else if (play) {
        gsap.to(words, anim)
      }
    }, el)

    return () => ctx.revert()
  }, [delay, stagger, trigger, play])

  return (
    // The text stays in the DOM verbatim so search engines and screen readers
    // read a sentence, not a pile of spans. Only the visual is split.
    <Tag ref={root} className={className} aria-label={text}>
      {text.split(' ').map((w, i) => (
        <span key={i} className="inline-block overflow-hidden align-bottom" aria-hidden>
          <span data-word className="inline-block">
            {w}
            {i < text.split(' ').length - 1 ? ' ' : ''}
          </span>
        </span>
      ))}
    </Tag>
  )
}

/** Fade + rise on scroll into view. */
export function FadeUp({
  children,
  className = '',
  delay = 0,
  y = 28,
}: {
  children: React.ReactNode
  className?: string
  delay?: number
  y?: number
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      gsap.set(el, { opacity: 1, y: 0 })
      return
    }
    const ctx = gsap.context(() => {
      gsap.fromTo(
        el,
        { opacity: 0, y },
        {
          opacity: 1, y: 0, duration: DUR, ease: EASE, delay,
          scrollTrigger: { trigger: el, start: 'top 90%', once: true },
        },
      )
    }, el)
    return () => ctx.revert()
  }, [delay, y])

  return <div ref={ref} className={className} style={{ opacity: 0 }}>{children}</div>
}

/**
 * Parallax by scroll position. `speed` is how far the element travels relative
 * to the scroll, negative to lead, positive to lag.
 */
export function Parallax({
  children,
  speed = 0.2,
  className = '',
}: {
  children: React.ReactNode
  speed?: number
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const ctx = gsap.context(() => {
      gsap.fromTo(
        el,
        { yPercent: -speed * 50 },
        {
          yPercent: speed * 50,
          ease: 'none',
          scrollTrigger: {
            trigger: el,
            start: 'top bottom',
            end: 'bottom top',
            // scrub with a small lag: instant tracking looks mechanical,
            // and this is what makes parallax feel like depth rather than
            // a transform bound to a scrollbar.
            scrub: 0.6,
          },
        },
      )
    }, el)
    return () => ctx.revert()
  }, [speed])

  return <div ref={ref} className={className}>{children}</div>
}

/** Button that leans toward the cursor. Pairs with the ring in Cursor.tsx. */
export function Magnetic({
  children,
  className = '',
  strength = 0.35,
}: {
  children: React.ReactNode
  className?: string
  strength?: number
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!window.matchMedia('(pointer: fine)').matches) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const move = (e: PointerEvent) => {
      const b = el.getBoundingClientRect()
      gsap.to(el, {
        x: (e.clientX - (b.left + b.width / 2)) * strength,
        y: (e.clientY - (b.top + b.height / 2)) * strength,
        duration: 0.7,
        ease: 'power3.out',
      })
    }
    // elastic on release: the snap back is most of the charm
    const leave = () => gsap.to(el, { x: 0, y: 0, duration: 1.1, ease: 'elastic.out(1, 0.4)' })

    el.addEventListener('pointermove', move)
    el.addEventListener('pointerleave', leave)
    return () => {
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerleave', leave)
      gsap.killTweensOf(el)
    }
  }, [strength])

  return <div ref={ref} className={className}>{children}</div>
}

export { ScrollTrigger }
