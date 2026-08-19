import React from 'react'
import '../styles/landing.css'

export default function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="min-h-screen flex items-center justify-center p-4 sm:p-8"
      style={{
        backgroundColor: 'var(--bg-color, #f2ede7)',
        color: 'var(--color-ink, #14100d)',
        fontFamily: "'Epilogue', sans-serif"
      }}
    >
      <div
        className="absolute inset-0 opacity-[0.2]"
        style={{
          backgroundImage: 'linear-gradient(rgba(255,107,0,0.05) 1px, transparent 1px), linear-gradient(90deg, rgba(255,107,0,0.05) 1px, transparent 1px)',
          backgroundSize: '40px 40px',
          pointerEvents: 'none'
        }}
      />

      <div className="w-full max-w-md relative z-10">
        {/* No forced aspect-square: that fixed the card's height to its width
            regardless of content, which is what was starving it of breathing
            room. Padding now does the spacing work instead. */}
        <div
          className="rounded-[32px] overflow-hidden"
          style={{
            backgroundColor: '#ffffff',
            boxShadow: '0 25px 50px -12px rgba(20, 16, 13, 0.10), 0 0 0 1px rgba(20, 16, 13, 0.04)',
          }}
        >
          <div className="px-10 py-14 sm:px-14 sm:py-16">
            {children}
          </div>
        </div>

        <div className="mt-10 text-center text-sm font-medium opacity-50 tracking-wide" style={{ color: 'var(--color-ink, #14100d)' }}>
          © {new Date().getFullYear()} VIGIL. All rights reserved.
        </div>
      </div>
    </div>
  )
}
