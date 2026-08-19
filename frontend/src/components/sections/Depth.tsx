'use client'

import Image from 'next/image'
import { Parallax, SplitReveal, FadeUp } from '../motion/Reveal'

/**
 * The atmospheric break — three plates drifting at different rates behind a
 * statement.
 *
 * Depth comes from *differential* speed, not from any one layer moving fast.
 * The plates travel at 0.35 / 0.55 / 0.18; the text sits nearly still. Push any
 * single layer harder and it stops reading as parallax and starts reading as a
 * thing sliding around.
 *
 * Images are remote and allow-listed in next.config (Unsplash, commercial use
 * permitted). Deliberately abstract — server rooms and fibre rather than stock
 * photos of people pointing at monitors, which is the exact generic register
 * this page is trying to avoid.
 */

const PLATES = [
  {
    src: 'https://images.unsplash.com/photo-1558494949-ef010cbdcc31?q=80&w=1600&auto=format&fit=crop',
    alt: '',
    speed: 0.35,
    className: 'left-[4%] top-[8%] w-[30vw] max-w-[380px] aspect-[3/4]',
  },
  {
    src: 'https://images.unsplash.com/photo-1451187580459-43490279c0fa?q=80&w=1600&auto=format&fit=crop',
    alt: '',
    speed: 0.55,
    className: 'right-[6%] top-[22%] w-[24vw] max-w-[300px] aspect-square',
  },
  {
    src: 'https://images.unsplash.com/photo-1620712943543-bcc4688e7485?q=80&w=1600&auto=format&fit=crop',
    alt: '',
    speed: 0.18,
    className: 'left-[16%] bottom-[6%] w-[22vw] max-w-[280px] aspect-[4/3]',
  },
]

export function Depth() {
  return (
    <section className="relative overflow-hidden py-[22vh]" style={{ background: '#080808' }}>
      {/* Plates sit behind and are decorative: empty alt, hidden from the
          accessibility tree. They carry no information. */}
      <div className="pointer-events-none absolute inset-0" aria-hidden>
        {PLATES.map((p, i) => (
          <Parallax key={i} speed={p.speed} className={`absolute ${p.className}`}>
            <div className="relative h-full w-full overflow-hidden rounded-xl">
              <Image
                src={p.src}
                alt={p.alt}
                fill
                sizes="(max-width: 1024px) 40vw, 30vw"
                className="object-cover opacity-[0.22] grayscale"
                // Below the fold by definition — never preload these.
                loading="lazy"
              />
              {/* Tint toward brand so the photography does not fight the palette */}
              <div className="absolute inset-0 bg-gradient-to-t from-[#080808] via-transparent to-[#080808]/60" />
              <div className="absolute inset-0 mix-blend-color bg-[#FF6B00]/25" />
            </div>
          </Parallax>
        ))}
      </div>

      {/* Statement */}
      <div className="relative z-10 mx-auto max-w-[1680px] px-[5vw] text-center">
        <FadeUp>
          <span className="font-mono text-[10px] uppercase tracking-[0.3em] text-[#FF6B00]">
            The position
          </span>
        </FadeUp>

        <SplitReveal
          as="h2"
          text="An agent you cannot stop is not autonomous. It is unsupervised."
          className="mx-auto mt-8 block max-w-[18ch] font-[900] leading-[0.92] tracking-[-0.045em]
                     text-[#FDFCF8] text-[10vw] lg:text-[4.6vw]"
          stagger={0.05}
        />

        <FadeUp delay={0.2}>
          <p className="mx-auto mt-10 max-w-[52ch] text-sm leading-relaxed text-white/40 md:text-base">
            Vigil does not make agents safer by making them weaker. It makes the
            boundary explicit, enforceable, and auditable — so the autonomy you
            grant is the autonomy you actually meant.
          </p>
        </FadeUp>
      </div>
    </section>
  )
}
