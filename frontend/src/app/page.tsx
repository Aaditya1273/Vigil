'use client'

import './styles/landing.css'

import SvgSymbols from '@/components/landing/SvgSymbols'
import SmoothScroll from '@/components/landing/SmoothScroll'
import CursorBubble from '@/components/landing/CursorBubble'
import Navbar from '@/components/landing/Navbar'
import VimeoHero from '@/components/landing/VimeoHero'
import HorizontalWords from '@/components/landing/HorizontalWords'
import MotionCards from '@/components/landing/MotionCards'
import Showreel from '@/components/landing/Showreel'
import ServiceCards from '@/components/landing/ServiceCards'
import DoubleMarquee from '@/components/landing/DoubleMarquee'
import Footer from '@/components/landing/Footer'
import TransitionScribble from '@/components/landing/TransitionScribble'

/**
 * Landing page.
 *
 * Structure and motion are a 1:1 port of the reference site in `refrance/`:
 * same section order, same GSAP timelines, same Lenis configuration, same
 * class names — so the feel is identical rather than approximated. What
 * changed is the palette (Vigil's orange/black on warm paper) and every string,
 * which is now the product rather than an advertising agency.
 *
 * The stylesheet is imported here rather than in globals.css so the dashboard
 * routes never load it: the landing page ships its own fonts and a full reset,
 * and leaking that into /mission-control would restyle the whole app.
 */
export default function Home() {
  return (
    <>
      <SvgSymbols />
      <SmoothScroll />
      <CursorBubble />

      <header className="main-header">
        <Navbar />
        <VimeoHero />
      </header>

      <HorizontalWords />

      <main>
        <div className="content-section motion-cards-wrapper">
          <MotionCards />
        </div>
        <Showreel />
        <div className="content-section service-cards-wrapper">
          <ServiceCards />
        </div>
      </main>

      <section className="Double-marquee">
        <DoubleMarquee />
      </section>

      <footer className="main-footer">
        <Footer />
      </footer>

      <TransitionScribble />
    </>
  )
}
