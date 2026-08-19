'use client'

import { useEffect, useRef } from 'react'
import { Renderer, Program, Mesh, Triangle } from 'ogl'

/**
 * The hero's background: tool calls flowing toward an interception plane.
 *
 * This is the product rendered literally rather than decorated. Traces stream
 * left to right; most cross the plane and continue as cool, quiet lines; a
 * minority strike it and detonate in orange. `blockRate` is the only real
 * control — driving it from scroll turns the hero into the pitch.
 *
 * Generative on purpose. The previous hero streamed a 12 MB MP4 from a
 * CloudFront URL nobody in this repo controls; a shader has no asset budget,
 * no external dependency, and no link to rot.
 */

const vertex = /* glsl */ `#version 300 es
in vec2 position;
void main() { gl_Position = vec4(position, 0.0, 1.0); }
`

const fragment = /* glsl */ `#version 300 es
precision highp float;

uniform float iTime;
uniform vec2  iResolution;
uniform float uBlockRate;   // 0..1 — share of traces refused at the plane
uniform float uIntensity;   // 0..1 — master opacity, used for the intro reveal
uniform vec2  uPointer;     // -1..1, parallax
out vec4 fragColor;

const vec3 ORANGE = vec3(1.000, 0.420, 0.000);   // #FF6B00
const vec3 EMBER  = vec3(1.000, 0.720, 0.380);
const vec3 COOL   = vec3(0.620, 0.680, 0.760);

float hash(float n) { return fract(sin(n) * 43758.5453123); }

float hash2(vec2 p) {
  return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

// Soft additive line with a bright core, so bloom reads without a blur pass.
float trace(vec2 uv, float y, float width) {
  float d = abs(uv.y - y);
  return width / (d + width) * smoothstep(width * 6.0, 0.0, d);
}

void main() {
  vec2 frag = gl_FragCoord.xy;
  vec2 uv = (frag - 0.5 * iResolution) / iResolution.y;

  // Parallax: the field drifts against the cursor, so the plane feels placed
  // in space rather than painted on.
  uv += uPointer * 0.035;

  // The interception plane sits right of centre, leaving room for the wordmark.
  float planeX = 0.42;

  vec3 col = vec3(0.0);
  float planeGlow = 0.0;

  const int LANES = 34;
  for (int i = 0; i < LANES; i++) {
    float fi = float(i);
    float seed = hash(fi * 12.9898);

    // Lane geometry
    float laneY = (fi / float(LANES) - 0.5) * 1.25 + (seed - 0.5) * 0.02;
    float speed = 0.16 + seed * 0.42;
    float width = 0.0016 + seed * 0.0032;

    // Each lane emits a packet on a loop; phase decorrelates the lanes.
    float cycle = 2.4 + seed * 3.0;
    float t = mod(iTime * speed + seed * cycle, cycle) / cycle;

    // Deterministic per-packet verdict. Each emission gets its own draw, so
    // the same lane alternates rather than being permanently allowed or
    // permanently blocked.
    float packet = floor((iTime * speed + seed * cycle) / cycle);
    float verdict = hash2(vec2(fi, packet));
    bool blocked = verdict < uBlockRate;

    // Travel: blocked packets decelerate into the plane and stop there.
    float startX = -0.95;
    float headX = mix(startX, 1.05, t);
    float impact = smoothstep(planeX - 0.02, planeX, headX);
    if (blocked) {
      headX = min(headX, planeX);
    }

    // Comet: bright head, fading tail behind it.
    float tail = smoothstep(headX - 0.34, headX, uv.x) * step(uv.x, headX);
    float head = smoothstep(headX - 0.02, headX, uv.x) * step(uv.x, headX + 0.004);
    float line = trace(uv, laneY, width) * (tail * 0.55 + head * 1.9);

    if (blocked) {
      // Refused: the packet burns at the boundary.
      float burn = impact;
      col += line * mix(COOL * 0.55, ORANGE, burn) * (0.6 + burn * 2.2);

      // Radial flare at the point of contact, decaying over the packet's life.
      float life = smoothstep(1.0, 0.55, t);
      float d = length((uv - vec2(planeX, laneY)) * vec2(1.0, 1.35));
      planeGlow += burn * life * 0.020 / (d + 0.020);
    } else {
      // Permitted: it crosses and cools on the far side.
      float past = smoothstep(planeX, planeX + 0.16, uv.x);
      col += line * mix(COOL, vec3(0.86, 0.90, 0.95), past) * 0.62;
    }
  }

  // The plane itself: a vertical seam, faint until something strikes it.
  float seam = 0.0012 / (abs(uv.x - planeX) + 0.0012);
  float breathe = 0.5 + 0.5 * sin(iTime * 0.7);
  col += ORANGE * seam * (0.16 + 0.10 * breathe + planeGlow * 0.35);
  col += EMBER * planeGlow;

  // Vignette, then film grain. Grain last so it sits on top of everything.
  float vig = 1.0 - dot(uv, uv) * 0.42;
  col *= clamp(vig, 0.0, 1.0);

  float grain = hash2(frag + fract(iTime) * 100.0) - 0.5;
  col += grain * 0.030;

  col *= uIntensity;

  // Alpha from luminance so the canvas composites over the page background
  // rather than needing to own it.
  float a = clamp(max(col.r, max(col.g, col.b)) * 1.25, 0.0, 1.0);
  fragColor = vec4(col, a);
}
`

export interface InterceptFieldProps {
  /** Share of traces refused at the plane, 0..1. */
  blockRate?: number
  /** Master opacity, for the intro reveal. */
  intensity?: number
  className?: string
}

export function InterceptField({ blockRate = 0.22, intensity = 1, className }: InterceptFieldProps) {
  const ref = useRef<HTMLDivElement>(null)

  // Targets live in a ref so scroll updates never tear down the WebGL context.
  // Synced in an effect rather than written during render: a ref mutation
  // during render is not safe under concurrent rendering, which may run a
  // render that is later discarded.
  const target = useRef({ blockRate, intensity })
  useEffect(() => {
    target.current = { blockRate, intensity }
  }, [blockRate, intensity])

  useEffect(() => {
    const container = ref.current
    if (!container) return

    // Respect the OS setting: a full-screen animated field is exactly what
    // prefers-reduced-motion exists for.
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

    const renderer = new Renderer({
      webgl: 2,
      alpha: true,
      antialias: false,
      dpr: Math.min(window.devicePixelRatio || 1, 2),
    })
    const gl = renderer.gl
    const canvas = gl.canvas as HTMLCanvasElement
    canvas.style.width = '100%'
    canvas.style.height = '100%'
    canvas.style.display = 'block'
    container.appendChild(canvas)

    const program = new Program(gl, {
      vertex,
      fragment,
      uniforms: {
        iTime: { value: 0 },
        iResolution: { value: new Float32Array([1, 1]) },
        uBlockRate: { value: blockRate },
        uIntensity: { value: 0 },
        uPointer: { value: new Float32Array([0, 0]) },
      },
    })
    const mesh = new Mesh(gl, { geometry: new Triangle(gl), program })

    const setSize = () => {
      const r = container.getBoundingClientRect()
      renderer.setSize(Math.max(1, r.width), Math.max(1, r.height))
      const res = (program.uniforms.iResolution as { value: Float32Array }).value
      res[0] = gl.drawingBufferWidth
      res[1] = gl.drawingBufferHeight
      renderer.render({ scene: mesh })
    }
    const ro = new ResizeObserver(setSize)
    ro.observe(container)
    setSize()

    const pointer = { x: 0, y: 0 }
    const onMove = (e: PointerEvent) => {
      pointer.x = (e.clientX / window.innerWidth) * 2 - 1
      pointer.y = -((e.clientY / window.innerHeight) * 2 - 1)
    }
    window.addEventListener('pointermove', onMove, { passive: true })

    let raf = 0
    let visible = true
    const t0 = performance.now()
    // Eased followers, so scroll-driven changes glide instead of snapping.
    let curBlock = blockRate
    let curIntensity = 0
    const ptr = (program.uniforms.uPointer as { value: Float32Array }).value

    const loop = (t: number) => {
      const time = (t - t0) * 0.001
      ;(program.uniforms.iTime as { value: number }).value = reduced ? 0 : time

      curBlock += (target.current.blockRate - curBlock) * 0.06
      curIntensity += (target.current.intensity - curIntensity) * 0.05
      ;(program.uniforms.uBlockRate as { value: number }).value = curBlock
      ;(program.uniforms.uIntensity as { value: number }).value = curIntensity

      ptr[0] += (pointer.x - ptr[0]) * 0.04
      ptr[1] += (pointer.y - ptr[1]) * 0.04

      renderer.render({ scene: mesh })
      raf = requestAnimationFrame(loop)
    }

    const start = () => { if (visible && !document.hidden && raf === 0) raf = requestAnimationFrame(loop) }
    const stop = () => { if (raf !== 0) { cancelAnimationFrame(raf); raf = 0 } }

    // Do not burn a GPU on a hero that has scrolled out of view.
    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting
      if (visible) start()
      else stop()
    }, { threshold: 0 })
    io.observe(container)
    const onVis = () => {
      if (document.hidden) stop()
      else start()
    }
    document.addEventListener('visibilitychange', onVis)
    start()

    return () => {
      stop()
      io.disconnect()
      ro.disconnect()
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pointermove', onMove)
      canvas.remove()
      gl.getExtension('WEBGL_lose_context')?.loseContext()
    }
    // Mount-only: live values are read through `target`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div ref={ref} className={className} aria-hidden />
}
