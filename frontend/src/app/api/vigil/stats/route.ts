import { NextResponse } from 'next/server'
import { api } from '@/lib/backend'

// The URL of the Go backend running on port 8080
const BACKEND_URL = api('/vigil/stats')

export async function GET() {
  try {
    const res = await fetch(BACKEND_URL, {
      cache: 'no-store', // Real-time data
    })
    
    if (!res.ok) {
      return NextResponse.json({ error: 'Backend returned an error' }, { status: res.status })
    }
    
    const data = await res.json()
    return NextResponse.json(data)
  } catch (error) {
    console.error('Error proxying to backend:', error)
    return NextResponse.json(
      { error: 'Failed to connect to VIGIL Go backend' }, 
      { status: 503 }
    )
  }
}
