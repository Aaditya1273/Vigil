'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import dynamic from 'next/dynamic'

// react-force-graph-2d touches `window` at module load (it wraps a canvas
// force-simulation), so it cannot be part of the server-rendered bundle —
// dynamic import with ssr:false is not an optional nicety here, it's what
// keeps Next.js from throwing on the server render pass.
const ForceGraph2D = dynamic(() => import('react-force-graph-2d'), { ssr: false })

export interface Triplet {
  source: { entity_id: string; name: string; type?: string; namespace?: string }
  relation: { canonical_predicate: string; context?: string }
  target: { entity_id: string; name: string; type?: string; namespace?: string }
}

export interface ChunkRelation {
  triplets: Triplet[]
  combined_context?: string
}

export interface GraphContext {
  chunk_relations?: ChunkRelation[]
  query_paths?: unknown[]
}

interface GraphNode {
  id: string
  name: string
  type?: string
  val: number
}
interface GraphLink {
  source: string
  target: string
  label: string
}

// Flattens HydraDB's chunk_relations into a de-duplicated node/link graph.
// Node size (val) grows with in/out degree, so a hub entity (a maintainer
// shared across packages, a person named by many aliases) visibly stands out
// — that centrality is real signal from the extracted graph, not decoration.
function toGraphData(ctx: GraphContext | undefined): { nodes: GraphNode[]; links: GraphLink[] } {
  const nodes = new Map<string, GraphNode>()
  const links: GraphLink[] = []
  const degree = new Map<string, number>()

  const bump = (id: string) => degree.set(id, (degree.get(id) || 0) + 1)

  for (const cr of ctx?.chunk_relations || []) {
    for (const t of cr.triplets || []) {
      if (!nodes.has(t.source.entity_id)) {
        nodes.set(t.source.entity_id, { id: t.source.entity_id, name: t.source.name, type: t.source.type, val: 1 })
      }
      if (!nodes.has(t.target.entity_id)) {
        nodes.set(t.target.entity_id, { id: t.target.entity_id, name: t.target.name, type: t.target.type, val: 1 })
      }
      bump(t.source.entity_id)
      bump(t.target.entity_id)
      links.push({ source: t.source.entity_id, target: t.target.entity_id, label: t.relation.canonical_predicate })
    }
  }

  for (const n of nodes.values()) n.val = 1 + Math.min(6, (degree.get(n.id) || 1) - 1)
  return { nodes: [...nodes.values()], links }
}

const TYPE_COLORS: Record<string, string> = {
  CONCEPT: '#ff6b00',
  PERSON: '#3d4a52',
  ORG: '#8f3a10',
}

export function EntityGraph({ graphContext, height = 420 }: { graphContext: GraphContext | undefined; height?: number }) {
  const data = useMemo(() => toGraphData(graphContext), [graphContext])
  const [hover, setHover] = useState<string | null>(null)

  // ForceGraph2D has no way to auto-fill its parent — without an explicit
  // width it measures the window/document instead, which is why this
  // rendered as a canvas wider than the page the first time (a real bug
  // caught by screenshotting it, not something visible in the code alone).
  // A ResizeObserver on the wrapping div is what makes the canvas actually
  // match its card, including when the card resizes (window resize, sidebar
  // toggle, grid reflow).
  const containerRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const ro = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  if (data.nodes.length === 0) {
    return (
      <div style={{ height }} className="flex items-center justify-center text-sm text-gray-400 border border-dashed border-gray-200 rounded-xl">
        No graph relationships in this result yet.
      </div>
    )
  }

  return (
    <div ref={containerRef} style={{ height }} className="relative border border-gray-100 rounded-xl overflow-hidden bg-[#fdfcfa]">
      {width > 0 && <ForceGraph2D
        graphData={data}
        width={width}
        height={height}
        nodeLabel={(n: any) => `${n.name}${n.type ? ` (${n.type})` : ''}`}
        nodeColor={(n: any) => TYPE_COLORS[n.type] || '#ff6b00'}
        nodeRelSize={4}
        linkColor={() => 'rgba(20,16,13,0.25)'}
        linkDirectionalArrowLength={4}
        linkDirectionalArrowRelPos={1}
        linkLabel={(l: any) => l.label}
        cooldownTicks={80}
        onNodeHover={(n: any) => setHover(n?.name ?? null)}
        nodeCanvasObjectMode={() => 'after'}
        nodeCanvasObject={(node: any, ctx: CanvasRenderingContext2D, scale: number) => {
          const label = node.name
          const fontSize = 11 / scale
          ctx.font = `${fontSize}px 'DM Sans', sans-serif`
          ctx.fillStyle = '#14100d'
          ctx.textAlign = 'center'
          ctx.textBaseline = 'top'
          ctx.fillText(label, node.x, node.y + 6 / scale)
        }}
      />}
      {hover && (
        <div className="absolute top-2 left-2 text-xs font-medium bg-white/90 border border-gray-200 rounded-lg px-2 py-1 pointer-events-none">
          {hover}
        </div>
      )}
    </div>
  )
}
