'use client'

import { useEffect, useRef, useState } from 'react'

/**
 * Live connection to the control plane's event stream.
 *
 * This block was previously copy-pasted into three pages with slightly
 * different reconnect behavior. One copy now, so a fix to the backoff is a fix
 * everywhere.
 *
 * The URL must come from an env var rather than the rewrite in next.config.ts:
 * Next rewrites do not proxy WebSocket upgrades, so there is no server-side
 * path to fall back on.
 */
const WS_URL =
  process.env.NEXT_PUBLIC_VIGIL_WS_URL ||
  'ws://localhost:8080/api/v1/vigil/ws'

export interface VigilEvent {
  type?: string
  event?: string
  [key: string]: unknown
}

/** Returns whether the socket is currently connected. */
export function useVigilSocket(onMessage: (msg: VigilEvent) => void): boolean {
  const [live, setLive] = useState(false)

  // Held in a ref so callers do not have to memoize their handler; a new
  // closure each render would otherwise tear down and rebuild the socket.
  const handler = useRef(onMessage)
  handler.current = onMessage

  useEffect(() => {
    let ws: WebSocket | null = null
    let retry = 1000
    let timer: ReturnType<typeof setTimeout> | null = null
    let closed = false

    const connect = () => {
      if (closed) return
      try {
        ws = new WebSocket(WS_URL)
      } catch {
        // Constructor throws on a malformed URL; retry rather than crash the page.
        timer = setTimeout(connect, retry)
        retry = Math.min(retry * 2, 30000)
        return
      }

      ws.onopen = () => {
        setLive(true)
        retry = 1000
      }

      ws.onmessage = (e) => {
        try {
          handler.current(JSON.parse(e.data))
        } catch {
          // A malformed frame is not worth breaking the stream over.
        }
      }

      ws.onclose = () => {
        setLive(false)
        if (closed) return
        timer = setTimeout(connect, retry)
        retry = Math.min(retry * 2, 30000)
      }

      ws.onerror = () => ws?.close()
    }

    connect()
    return () => {
      closed = true
      if (timer) clearTimeout(timer)
      ws?.close()
    }
  }, [])

  return live
}
