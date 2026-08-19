import { NextResponse } from 'next/server'
import { api } from '@/lib/backend'

const BACKEND_URL = api('/vigil/dna/profiles')

export async function GET() {
  try {
    const res = await fetch(BACKEND_URL, { cache: 'no-store' })
    if (!res.ok) {
      return NextResponse.json([], { status: 200 }) // return empty array, not error
    }
    const data = await res.json()
    // backend returns array directly
    return NextResponse.json(Array.isArray(data) ? data : [])
  } catch {
    return NextResponse.json([]) // graceful empty on backend down
  }
}
