'use client'

import { createContext, useCallback, useContext, useEffect, useRef } from 'react'
import Lenis from 'lenis'
import { gsap, ScrollTrigger } from '@/lib/gsap'

/**
 * One scroll loop for the whole page.
 *
 * Lenis and GSAP both want to own a RAF. Left alone they run on separate
 * clocks, ScrollTrigger reads a scroll position Lenis has already moved past,
 * and every scrubbed animation lags a frame behind the content — the exact
 * judder that makes a site feel cheap. So: Lenis is driven *by* GSAP's ticker,
 * and ScrollTrigger is told to ask Lenis for the scroll position. One clock,
 * one source of truth.
 *
 * The instance lives in a ref rather than state. It is an external system, not
 * rendered data, and putting it in state would mean a re-render of the whole
 * tree the moment it initialises for no visual gain.
 */

interface SmoothCtx {
  lenis: React.RefObject<Lenis | null>
  /** Signed scroll velocity, for velocity-reactive elements. */
  velocity: React.RefObject<number>
  /** Stop/start scrolling. Safe to call before Lenis exists. */
  setLocked: (locked: boolean) => void
}

const Ctx = createContext<SmoothCtx>({
  lenis: { current: null },
  velocity: { current: 0 },
  setLocked: () => {},
})

export const useSmooth = () => useContext(Ctx)

export function SmoothScroll({ children }: { children: React.ReactNode }) {
  const lenis = useRef<Lenis | null>(null)
  const velocity = useRef(0)
  const locked = useRef(false)

  // Applies the desired lock state to whatever exists right now. Called both
  // by consumers and by setup, so a lock requested before Lenis initialises is
  // not silently dropped — which is exactly the case on first paint, where the
  // curtain locks scrolling before anything else has run.
  const applyLock = useCallback(() => {
    const l = lenis.current
    if (!l) return
    if (locked.current) l.stop()
    else l.start()
  }, [])

  const setLocked = useCallback(
    (v: boolean) => {
      locked.current = v
      applyLock()
      // With reduced motion there is no Lenis to stop, so fall back to the
      // document. Otherwise a locked page would still scroll.
      document.documentElement.style.overflow = v && !lenis.current ? 'hidden' : ''
    },
    [applyLock],
  )

  useEffect(() => {
    // A user who asked the OS for less motion should get native scrolling, not
    // an eased hijack of it. ScrollTrigger still works against native scroll.
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      return () => {
        ScrollTrigger.getAll().forEach(t => t.kill())
      }
    }

    const l = new Lenis({
      // ~1s to settle: long enough to read as weight, short enough that the
      // page still feels like it obeys you.
      duration: 1.1,
      easing: (t: number) => Math.min(1, 1.001 - Math.pow(2, -10 * t)),
      orientation: 'vertical',
      smoothWheel: true,
      // Touch devices already have momentum scrolling in hardware. Layering
      // ours on top fights the platform and feels worse, not better.
      syncTouch: false,
      wheelMultiplier: 1,
      touchMultiplier: 1.6,
    })

    l.on('scroll', (e: { velocity: number }) => {
      velocity.current = e.velocity
      ScrollTrigger.update()
    })

    // Drive Lenis from GSAP's ticker rather than letting it run its own RAF.
    const raf = (time: number) => l.raf(time * 1000)
    gsap.ticker.add(raf)
    gsap.ticker.lagSmoothing(0)

    // Deliberately NO scrollerProxy. Lenis scrolls the real window, so
    // ScrollTrigger already reads the correct position natively — and adding a
    // proxy creates a feedback loop: ScrollTrigger's pin machinery writes
    // scrollTop, Lenis emits a scroll for that write, ScrollTrigger updates and
    // writes again. Measured before this was removed: the page careened from
    // 0 to 3600px and back on its own within three seconds of load.
    //
    // A proxy is only required when Lenis wraps a custom scroll container.
    ScrollTrigger.addEventListener('refresh', () => l.resize())

    lenis.current = l
    applyLock() // honour a lock requested before this ran
    ScrollTrigger.refresh()

    return () => {
      gsap.ticker.remove(raf)
      ScrollTrigger.getAll().forEach(t => t.kill())
      l.destroy()
      lenis.current = null
    }
  }, [applyLock])

  return <Ctx.Provider value={{ lenis, velocity, setLocked }}>{children}</Ctx.Provider>
}

/** Pause scrolling (for a curtain or an open overlay) without losing position. */
export function useScrollLock(isLocked: boolean) {
  const { setLocked } = useSmooth()
  useEffect(() => {
    setLocked(isLocked)
    return () => setLocked(false)
  }, [isLocked, setLocked])
}
