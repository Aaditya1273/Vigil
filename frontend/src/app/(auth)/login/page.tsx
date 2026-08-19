'use client'

import React, { useState } from 'react'
import { signIn } from 'next-auth/react'

export default function LoginPage() {
  const [loading, setLoading] = useState(false)
  
  const handleGoogleLogin = async () => {
    setLoading(true)
    try {
      await signIn('google', { callbackUrl: '/mission-control' })
    } catch (error) {
      console.error('Login failed:', error)
      setLoading(false)
    }
  }

  return (
    <div className="space-y-10 flex flex-col items-center">
      <div className="text-center w-full">
        <div className="flex items-center justify-center mx-auto mb-8">
          <span
            className="logo-truus"
            style={{
              fontSize: '48px',
              color: 'var(--color-ink, #14100d)',
              letterSpacing: '-2px',
              fontFamily: "'Epilogue', sans-serif",
              fontWeight: 900
            }}
          >
            VIGIL
          </span>
        </div>
        <h2
          className="text-3xl font-bold tracking-tight mb-4"
          style={{ color: 'var(--color-ink, #14100d)' }}
        >
          Join the Control Plane
        </h2>
        <p style={{ color: 'var(--color-slate, #3d4a52)', fontSize: '16px', fontWeight: 500, opacity: 0.8 }}>
          We judge every call before it runs.
        </p>
      </div>

      <div className="w-full pt-2">
        <button
          onClick={handleGoogleLogin}
          disabled={loading}
          className="btn-auth w-full flex justify-center items-center gap-4 py-4 px-8 font-bold"
          style={{
            fontSize: '15px',
            letterSpacing: '0.5px',
            color: '#ffffff',
            border: 'none',
            cursor: loading ? 'not-allowed' : 'url("/assets/Cursor SVG/cursor-pointer.svg") 12 12, pointer',
          }}
        >
          {loading ? (
            <div className="w-5 h-5 border-2 border-current border-t-transparent rounded-full animate-spin" />
          ) : (
            <>
              <svg className="w-5 h-5" viewBox="0 0 24 24" style={{ backgroundColor: 'white', borderRadius: '50%', padding: '2px' }}>
                <path
                  fill="#EA4335"
                  d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
                />
                <path
                  fill="#34A853"
                  d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
                />
                <path
                  fill="#4A90E2"
                  d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
                />
                <path
                  fill="#FBBC05"
                  d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
                />
              </svg>
              Continue with Google
            </>
          )}
        </button>
      </div>

    </div>
  )
}
