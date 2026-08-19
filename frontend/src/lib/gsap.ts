import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'

/**
 * Single registration point for GSAP plugins.
 *
 * Registering inside a component effect does not work here: React runs child
 * effects before parent effects, so a section that creates a ScrollTrigger
 * mounts and fires *before* the provider that would have registered the plugin
 * — and `new ScrollTrigger()` throws `_context is not a function`.
 *
 * Module bodies evaluate at import time, before any effect, so doing it here
 * means every consumer gets a registered plugin no matter the mount order.
 * Everything in the app must import gsap and ScrollTrigger from this file
 * rather than from the package directly.
 */
if (typeof window !== 'undefined') {
  gsap.registerPlugin(ScrollTrigger)
}

export { gsap, ScrollTrigger }
