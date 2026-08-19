'use client'

import { useEffect, useRef } from 'react'

/**
 * Custom cursor: a small dot that leads, and a ring that trails and snaps.
 *
 * Two rules make this feel expensive rather than gimmicky. The dot tracks the
 * pointer exactly — input must never feel laggy — while only the ring eases
 * behind it, which is what reads as weight. And over anything interactive the
 * ring *magnetises* to that element's centre instead of merely growing, so the
 * cursor appears to lock on.
 *
 * Pointer-fine only. A touch device has no cursor to replace, and a coarse
 * pointer would leave a ring stranded wherever the last tap landed.
 */
export function Cursor() {
  const dot = useRef<HTMLDivElement>(null)
  const ring = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!window.matchMedia('(pointer: fine)').matches) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const d = dot.current
    const r = ring.current
    if (!d || !r) return

    let mx = window.innerWidth / 2
    let my = window.innerHeight / 2
    let rx = mx
    let ry = my
    let scale = 1
    let targetScale = 1
    let magnet: HTMLElement | null = null
    let raf = 0

    const onMove = (e: PointerEvent) => {
      mx = e.clientX
      my = e.clientY

      // `closest` walks up, so a click on an icon inside a button still finds
      // the button — magnetising to the icon would feel arbitrary.
      const el = (e.target as HTMLElement)?.closest?.(
        'a, button, [role="button"], input, textarea, select, [data-cursor]',
      ) as HTMLElement | null

      magnet = el
      targetScale = el ? 2.4 : 1
    }

    const onDown = () => { targetScale *= 0.8 }
    const onUp = () => { targetScale = magnet ? 2.4 : 1 }
    const onLeave = () => { d.style.opacity = '0'; r.style.opacity = '0' }
    const onEnter = () => { d.style.opacity = '1'; r.style.opacity = '1' }

    const tick = () => {
      let tx = mx
      let ty = my

      if (magnet) {
        // Pull the ring most of the way to the element's centre — full snap
        // loses the connection to where the pointer actually is.
        const b = magnet.getBoundingClientRect()
        tx = mx + (b.left + b.width / 2 - mx) * 0.35
        ty = my + (b.top + b.height / 2 - my) * 0.35
      }

      rx += (tx - rx) * 0.16
      ry += (ty - ry) * 0.16
      scale += (targetScale - scale) * 0.14

      d.style.transform = `translate3d(${mx}px, ${my}px, 0) translate(-50%, -50%)`
      r.style.transform = `translate3d(${rx}px, ${ry}px, 0) translate(-50%, -50%) scale(${scale})`

      raf = requestAnimationFrame(tick)
    }

    window.addEventListener('pointermove', onMove, { passive: true })
    window.addEventListener('pointerdown', onDown, { passive: true })
    window.addEventListener('pointerup', onUp, { passive: true })
    document.addEventListener('pointerleave', onLeave)
    document.addEventListener('pointerenter', onEnter)
    raf = requestAnimationFrame(tick)

    document.documentElement.classList.add('has-custom-cursor')

    return () => {
      cancelAnimationFrame(raf)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('pointerup', onUp)
      document.removeEventListener('pointerleave', onLeave)
      document.removeEventListener('pointerenter', onEnter)
      document.documentElement.classList.remove('has-custom-cursor')
    }
  }, [])

  return (
    <>
      <div
        ref={dot}
        className="pointer-events-none fixed left-0 top-0 z-[9999] hidden h-[5px] w-[5px] rounded-full bg-[#FF6B00] [@media(pointer:fine)]:block"
        style={{ willChange: 'transform' }}
      />
      <div
        ref={ring}
        className="pointer-events-none fixed left-0 top-0 z-[9998] hidden h-8 w-8 rounded-full border border-white/25 [@media(pointer:fine)]:block"
        style={{ willChange: 'transform', mixBlendMode: 'difference' }}
      />
    </>
  )
}
