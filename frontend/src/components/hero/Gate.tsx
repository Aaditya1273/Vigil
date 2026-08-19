'use client'

import { useEffect, useRef } from 'react'
import * as THREE from 'three'

/**
 * The Gate — the hero's 3D scene.
 *
 * A dense stream of tool calls travels toward a vertical barrier. Most pass
 * through and cool to grey on the far side. A minority strike the plane and
 * detonate in orange. The camera drifts, and on scroll it pushes *through* the
 * barrier so you end up standing behind the firewall looking back at it.
 *
 * Built on raw Points + a custom shader rather than a particle library: 20k
 * particles need per-vertex GPU work, and the whole behaviour is four lines of
 * GLSL. A library would add weight to do less.
 *
 * Bloom is faked with additive blending and a soft radial falloff instead of a
 * post-processing pass. A real UnrealBloomPass means EffectComposer, two extra
 * render targets, and a measurable frame cost — for a look that is close to
 * indistinguishable at this scale.
 */

const COUNT = 11000
const PLANE_Z = 0

const vertexShader = /* glsl */ `
  uniform float uTime;
  uniform float uBlockRate;
  uniform float uScroll;
  uniform float uSize;

  attribute float aSeed;
  attribute float aSpeed;
  attribute float aScale;

  varying float vBlocked;
  varying float vImpact;
  varying float vDepth;

  // Cheap deterministic hash — every particle's verdict must be stable across
  // frames, so this cannot use time as an input.
  float hash(vec2 p) {
    return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
  }

  void main() {
    vec3 pos = position;

    // March along Z toward the plane, looping per particle.
    float span = 34.0;
    float t = fract((uTime * aSpeed * 0.06) + aSeed);
    float z = -span * 0.62 + t * span;

    // Each pass through the loop is a new "packet" and gets its own verdict,
    // so a particle alternates rather than being permanently one or the other.
    float packet = floor((uTime * aSpeed * 0.06) + aSeed);
    vBlocked = step(hash(vec2(aSeed * 91.7, packet)), uBlockRate);

    // Blocked particles decelerate into the barrier and stop dead at it.
    float stopped = min(z, PLANE_Z_CONST);
    z = mix(z, stopped, vBlocked);

    // How close this particle is to the moment of contact.
    vImpact = vBlocked * smoothstep(-2.5, 0.0, z);

    // Impact scatter: refused packets spray outward as they burn.
    float spray = vImpact * vImpact;
    float ang = aSeed * 6.2831;
    pos.x += cos(ang) * spray * 1.4 * aScale;
    pos.y += sin(ang) * spray * 1.4 * aScale;

    // Slow lateral drift keeps the field alive when nothing is being blocked.
    pos.x += sin(uTime * 0.16 + aSeed * 9.0) * 0.16;
    pos.y += cos(uTime * 0.13 + aSeed * 7.0) * 0.16;

    pos.z = z;

    vec4 mv = modelViewMatrix * vec4(pos, 1.0);
    vDepth = -mv.z;

    // Perspective-correct point size, with a floor so distant particles stay
    // visible as dust rather than vanishing into aliasing.
    gl_PointSize = max(1.0, (uSize * aScale * (1.0 + vImpact * 2.2)) * (34.0 / vDepth));
    gl_Position = projectionMatrix * mv;
  }
`.replace(/PLANE_Z_CONST/g, PLANE_Z.toFixed(1))

const fragmentShader = /* glsl */ `
  precision highp float;

  uniform vec3 uOrange;
  uniform vec3 uCool;
  uniform float uOpacity;

  varying float vBlocked;
  varying float vImpact;
  varying float vDepth;

  void main() {
    // Round sprite with a soft edge — the falloff is what reads as glow once
    // these are blended additively.
    vec2 c = gl_PointCoord - 0.5;
    float d = length(c);
    if (d > 0.5) discard;
    float alpha = smoothstep(0.5, 0.0, d);
    alpha = pow(alpha, 2.3);

    // Refused packets ignite; permitted ones stay cool.
    vec3 col = mix(uCool, uOrange, vImpact);
    col += uOrange * vImpact * 0.9;          // hot core at the point of contact
    col = mix(col, col * 1.15, vBlocked);

    // Fade with distance so the field has depth instead of a flat wall.
    float fog = smoothstep(60.0, 8.0, vDepth);

    gl_FragColor = vec4(col, alpha * uOpacity * fog * 0.85);
  }
`

export interface GateProps {
  /** 0..1 — share of packets refused at the barrier. */
  blockRate?: number
  /** 0..1 — scroll progress; pushes the camera through the plane. */
  scroll?: number
  /** 0..1 — master opacity, for the intro reveal. */
  intensity?: number
  className?: string
}

export function Gate({ blockRate = 0.2, scroll = 0, intensity = 1, className }: GateProps) {
  const host = useRef<HTMLDivElement>(null)
  const target = useRef({ blockRate, scroll, intensity })
  useEffect(() => {
    target.current = { blockRate, scroll, intensity }
  }, [blockRate, scroll, intensity])

  useEffect(() => {
    const el = host.current
    if (!el) return

    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    // Fewer particles on small screens: a phone GPU rendering 18k additive
    // points drops frames, and dropped frames are worse than fewer particles.
    const count = window.innerWidth < 768 ? Math.floor(COUNT * 0.35) : COUNT

    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(46, 1, 0.1, 200)
    camera.position.set(17, 3.5, 17)

    const renderer = new THREE.WebGLRenderer({ alpha: true, antialias: false, powerPreference: 'high-performance' })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    renderer.setClearColor(0x000000, 0)
    el.appendChild(renderer.domElement)
    renderer.domElement.style.cssText = 'width:100%;height:100%;display:block'

    // ── Particle field ──────────────────────────────────────────────────────
    const geo = new THREE.BufferGeometry()
    const positions = new Float32Array(count * 3)
    const seeds = new Float32Array(count)
    const speeds = new Float32Array(count)
    const scales = new Float32Array(count)

    for (let i = 0; i < count; i++) {
      // Disc distribution, denser toward the centre, so the stream reads as a
      // beam rather than a uniform box.
      // Tighter beam, hollowed slightly in the middle so the barrier's centre
      // stays readable instead of being packed solid.
      const r = (0.18 + Math.pow(Math.random(), 0.8) * 0.82) * 8.5
      const a = Math.random() * Math.PI * 2
      positions[i * 3] = Math.cos(a) * r
      positions[i * 3 + 1] = Math.sin(a) * r * 0.7
      positions[i * 3 + 2] = 0
      seeds[i] = Math.random()
      speeds[i] = 0.55 + Math.random() * 1.5
      scales[i] = 0.3 + Math.pow(Math.random(), 3.0) * 1.4   // a few big, many small
    }

    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3))
    geo.setAttribute('aSeed', new THREE.BufferAttribute(seeds, 1))
    geo.setAttribute('aSpeed', new THREE.BufferAttribute(speeds, 1))
    geo.setAttribute('aScale', new THREE.BufferAttribute(scales, 1))

    const uniforms = {
      uTime: { value: 0 },
      uBlockRate: { value: blockRate },
      uScroll: { value: 0 },
      uSize: { value: 9.0 },
      uOpacity: { value: 0 },
      uOrange: { value: new THREE.Color('#FF6B00') },
      uCool: { value: new THREE.Color('#A8BEDC') },
    }

    const mat = new THREE.ShaderMaterial({
      vertexShader,
      fragmentShader,
      uniforms,
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    })
    scene.add(new THREE.Points(geo, mat))

    // ── The barrier ─────────────────────────────────────────────────────────
    // A thin emissive disc, brightest at its rim, so it reads as a field rather
    // than a solid object you could not pass through.
    const ring = new THREE.Mesh(
      new THREE.RingGeometry(0.1, 13, 128),
      new THREE.ShaderMaterial({
        transparent: true,
        depthWrite: false,
        side: THREE.DoubleSide,
        blending: THREE.AdditiveBlending,
        uniforms: {
          uTime: { value: 0 },
          uOpacity: { value: 0 },
          uHeat: { value: 0 },
          uOrange: { value: new THREE.Color('#FF6B00') },
        },
        vertexShader: `
          varying vec2 vUv;
          varying float vR;
          void main() {
            vUv = uv;
            vR = length(position.xy);
            gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
          }
        `,
        fragmentShader: `
          precision highp float;
          uniform float uTime; uniform float uOpacity; uniform float uHeat; uniform vec3 uOrange;
          varying float vR;
          void main() {
            float edge = smoothstep(10.0, 7.0, vR);        // fade toward the rim
            float core = smoothstep(0.0, 4.5, vR);          // hollow centre
            // Concentric interference, drifting outward — the field is working.
            float rings = 0.5 + 0.5 * sin(vR * 3.2 - uTime * 1.1);
            float a = edge * core * (0.06 + rings * 0.05 + uHeat * 0.22);
            gl_FragColor = vec4(uOrange * (1.0 + uHeat * 1.6), a * uOpacity);
          }
        `,
      }),
    )
    ring.position.z = PLANE_Z
    ring.scale.y = 0.7
    scene.add(ring)

    const ringMat = ring.material as THREE.ShaderMaterial

    // ── Sizing ──────────────────────────────────────────────────────────────
    const resize = () => {
      const r = el.getBoundingClientRect()
      const w = Math.max(1, r.width)
      const h = Math.max(1, r.height)
      renderer.setSize(w, h, false)
      camera.aspect = w / h
      camera.updateProjectionMatrix()
    }
    const ro = new ResizeObserver(resize)
    ro.observe(el)
    resize()

    // ── Pointer parallax ────────────────────────────────────────────────────
    const pointer = { x: 0, y: 0 }
    const onMove = (e: PointerEvent) => {
      pointer.x = (e.clientX / window.innerWidth) * 2 - 1
      pointer.y = -((e.clientY / window.innerHeight) * 2 - 1)
    }
    window.addEventListener('pointermove', onMove, { passive: true })

    // ── Loop ────────────────────────────────────────────────────────────────
    let raf = 0
    let visible = true
    const clock = new THREE.Timer()
    // Eased followers: props change in steps, the scene must not.
    let curBlock = blockRate
    let curScroll = 0
    let curOpacity = 0
    let heat = 0
    const camTarget = new THREE.Vector3()

    const tick = () => {
      clock.update()
      const t = reduced ? 0 : clock.getElapsed()
      const tgt = target.current

      curBlock += (tgt.blockRate - curBlock) * 0.045
      curScroll += (tgt.scroll - curScroll) * 0.06
      curOpacity += (tgt.intensity - curOpacity) * 0.04
      // Heat trails the block rate, so a surge of refusals makes the barrier
      // flare and then settle rather than tracking instantly.
      heat += (curBlock - heat) * 0.03

      uniforms.uTime.value = t
      uniforms.uBlockRate.value = curBlock
      uniforms.uScroll.value = curScroll
      uniforms.uOpacity.value = curOpacity
      ringMat.uniforms.uTime.value = t
      ringMat.uniforms.uOpacity.value = curOpacity
      ringMat.uniforms.uHeat.value = heat

      // Scroll swings the camera around and through the barrier: it starts
      // off to one side watching traffic arrive, and ends behind the plane
      // looking back down the stream.
      const orbit = curScroll * 1.15
      const radius = 24 - curScroll * 9
      camTarget.set(
        Math.cos(orbit + 0.79) * radius + pointer.x * 1.6,
        3.5 - curScroll * 2.5 + pointer.y * 1.0,
        Math.sin(orbit + 0.79) * radius,
      )
      camera.position.lerp(camTarget, 0.04)
      camera.lookAt(0, 0, PLANE_Z)

      renderer.render(scene, camera)
      raf = requestAnimationFrame(tick)
    }

    const start = () => { if (visible && !document.hidden && raf === 0) raf = requestAnimationFrame(tick) }
    const stop = () => { if (raf !== 0) { cancelAnimationFrame(raf); raf = 0 } }

    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting
      if (visible) start()
      else stop()
    }, { threshold: 0 })
    io.observe(el)

    const onVis = () => { if (document.hidden) stop(); else start() }
    document.addEventListener('visibilitychange', onVis)
    start()

    return () => {
      stop()
      io.disconnect()
      ro.disconnect()
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pointermove', onMove)
      geo.dispose()
      mat.dispose()
      ring.geometry.dispose()
      ringMat.dispose()
      renderer.dispose()
      renderer.domElement.remove()
    }
    // Mount-only; live values arrive through `target`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div ref={host} className={className} aria-hidden />
}
