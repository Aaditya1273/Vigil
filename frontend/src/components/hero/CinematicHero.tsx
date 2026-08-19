'use client'

import { useRef, useState } from 'react'
import { motion, useScroll, useTransform, useMotionValueEvent } from 'framer-motion'
import { InterceptField } from './InterceptField'
import { DecisionTicker } from './DecisionTicker'
import { Preloader } from './Preloader'

/** Mask-reveal for a headline, one word per line, rising out of its own clip. */
function RevealLine({ children, delay = 0, play = true }: { children: React.ReactNode; delay?: number; play?: boolean }) {
  return (
    <span className="block overflow-hidden">
      <motion.span
        className="block"
        initial={{ y: '110%' }}
        animate={play ? { y: 0 } : { y: '110%' }}
        transition={{ duration: 1.1, delay, ease: [0.16, 1, 0.3, 1] }}
      >
        {children}
      </motion.span>
    </span>
  )
}

export function CinematicHero() {
  const [ready, setReady] = useState(false)
  const wrap = useRef<HTMLDivElement>(null)

  // Pin the hero for two viewport heights so the scroll has room to tell a story.
  const { scrollYProgress } = useScroll({ target: wrap, offset: ['start start', 'end start'] })

  // As you scroll, the firewall tightens: more traffic is refused. The
  // background stops being decoration and becomes the argument.
  const [blockRate, setBlockRate] = useState(0.18)
  useMotionValueEvent(scrollYProgress, 'change', v => setBlockRate(0.18 + v * 0.62))

  const titleY = useTransform(scrollYProgress, [0, 1], ['0%', '-38%'])
  const titleOpacity = useTransform(scrollYProgress, [0, 0.75], [1, 0])
  const subOpacity = useTransform(scrollYProgress, [0, 0.35], [1, 0])

  // `ready` alone gates the reveal — content waits for the curtain so the
  // animation is not spent behind it. A second piece of state mirroring
  // `ready` would just be a render cascade with extra steps.

  return (
    <>
      <Preloader onDone={() => setReady(true)} />

      <div ref={wrap} className="relative h-[200vh]" style={{ background: '#0A0A0A' }}>
        <div className="sticky top-0 h-screen w-full overflow-hidden">
          {/* The field */}
          <InterceptField
            blockRate={blockRate}
            intensity={ready ? 1 : 0}
            className="absolute inset-0"
          />

          {/* Legibility floor under the type, without flattening the field. */}
          <div className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#0A0A0A] via-[#0A0A0A]/45 to-transparent" />
          <div className="pointer-events-none absolute inset-0 bg-gradient-to-r from-[#0A0A0A]/85 via-transparent to-transparent" />

          {/* Top rail */}
          <motion.div
            className="absolute top-0 left-0 right-0 z-20 flex items-center justify-between px-[5vw] py-7"
            initial={{ opacity: 0, y: -12 }}
            animate={ready ? { opacity: 1, y: 0 } : {}}
            transition={{ duration: 0.9, delay: 0.5, ease: [0.16, 1, 0.3, 1] }}
          >
            <span className="font-[900] text-white tracking-tight text-lg">VIGIL</span>
            <span className="hidden md:block font-mono text-[10px] tracking-[0.25em] uppercase text-white/35">
              Runtime firewall for autonomous agents
            </span>
          </motion.div>

          {/* Headline block */}
          <motion.div
            className="absolute inset-x-0 bottom-0 z-10 px-[5vw] pb-[7vh]"
            style={{ y: titleY, opacity: titleOpacity }}
          >
            <div className="max-w-[1680px] mx-auto">
              {/* Always rendered, never conditionally mounted: the heading must
                  be in the server markup for search engines and screen readers.
                  The reveal is the mask animating, not the element appearing. */}
              <h1
                className="font-[900] text-[#FDFCF8] leading-[0.82] tracking-[-0.055em]
                           text-[15vw] md:text-[12vw] lg:text-[10.5vw]"
              >
                <RevealLine delay={0.15} play={ready}>EVERY CALL</RevealLine>
                <RevealLine delay={0.28} play={ready}>
                  <span className="text-[#FF6B00]">JUDGED</span> BEFORE
                </RevealLine>
                <RevealLine delay={0.41} play={ready}>IT RUNS.</RevealLine>
              </h1>

              <motion.div
                className="mt-8 flex flex-col lg:flex-row items-start lg:items-end justify-between gap-10"
                style={{ opacity: subOpacity }}
                initial={{ opacity: 0 }}
                animate={ready ? { opacity: 1 } : {}}
                transition={{ duration: 0.9, delay: 0.95 }}
              >
                <p className="max-w-[440px] text-white/60 text-sm md:text-base leading-relaxed">
                  Vigil sits between an autonomous agent and its tools. Declared intent,
                  live cost, and behavioural baseline are checked on every call —
                  before execution, not after the damage.
                </p>

                <DecisionTicker />
              </motion.div>
            </div>
          </motion.div>

          {/* Scroll affordance */}
          <motion.div
            className="absolute bottom-6 left-1/2 -translate-x-1/2 z-20 flex flex-col items-center gap-2"
            initial={{ opacity: 0 }}
            animate={ready ? { opacity: 1 } : {}}
            transition={{ delay: 1.6, duration: 0.8 }}
            style={{ opacity: subOpacity }}
          >
            <span className="font-mono text-[9px] tracking-[0.3em] uppercase text-white/25">
              Tighten the firewall
            </span>
            <motion.span
              className="w-px h-8 bg-gradient-to-b from-[#FF6B00] to-transparent"
              animate={{ scaleY: [0.35, 1, 0.35], transformOrigin: 'top' }}
              transition={{ duration: 2.2, repeat: Infinity, ease: 'easeInOut' }}
            />
          </motion.div>

          {/* Live block-rate readout: the scroll is doing something measurable,
              and showing the number is more persuasive than implying it. */}
          <motion.div
            className="absolute right-[5vw] top-1/2 -translate-y-1/2 z-20 hidden lg:block text-right"
            initial={{ opacity: 0 }}
            animate={ready ? { opacity: 1 } : {}}
            transition={{ delay: 1.3, duration: 0.8 }}
          >
            <p className="font-mono text-[9px] tracking-[0.3em] uppercase text-white/25 mb-1">Refused</p>
            <p className="font-[900] text-[#FF6B00] text-5xl tabular-nums leading-none">
              {Math.round(blockRate * 100)}<span className="text-2xl">%</span>
            </p>
          </motion.div>
        </div>
      </div>
    </>
  )
}
