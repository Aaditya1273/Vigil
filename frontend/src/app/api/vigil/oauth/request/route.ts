import { NextRequest, NextResponse } from 'next/server'
import { api } from '@/lib/backend'

export async function GET(req: NextRequest) {
  const id = req.nextUrl.searchParams.get('id')
  if (!id) return NextResponse.json({ error: 'id required' }, { status: 400 })
  try {
    const res = await fetch(api(`/vigil/oauth/request?id=${encodeURIComponent(id)}`), {
      cache: 'no-store',
    })
    const data = await res.json()
    return NextResponse.json(data, { status: res.status })
  } catch {
    return NextResponse.json({ error: 'backend unavailable' }, { status: 503 })
  }
}
