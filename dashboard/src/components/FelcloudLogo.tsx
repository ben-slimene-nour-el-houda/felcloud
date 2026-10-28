import React from 'react'

interface LogoProps {
  className?: string
  size?: number | string
  glow?: boolean
  showWordmark?: boolean
  wordmarkColor?: string
}

/**
 * FelCloud Official Logo Component
 * High-definition, crisp SVG vector tracing matching the official company brand.
 */
export default function FelcloudLogo({
  className = '',
  size = 40,
  glow = false,
  showWordmark = false,
  wordmarkColor = 'text-white',
}: LogoProps) {
  const width = typeof size === 'number' ? size : size
  const height = typeof size === 'number' ? Math.round((size * 68) / 100) : 'auto'

  return (
    <div className={`inline-flex items-center gap-3 select-none ${className}`}>
      <div className="relative inline-flex items-center justify-center flex-shrink-0">
        {glow && (
          <div
            className="absolute inset-0 bg-[#FFD200]/25 blur-lg rounded-full scale-125 pointer-events-none"
            aria-hidden="true"
          />
        )}
        <svg
          width={width}
          height={height}
          viewBox="0 0 160 110"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
          className="relative z-10 transition-transform duration-300"
          style={{ overflow: 'visible' }}
        >
          {/* Main Gold Lineart Shapes */}
          <g
            stroke="#FFD200"
            strokeWidth="11"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            {/* 1. Left Stadium Loop (Starts at top shelf, arcs around left, ends at bottom shelf before slit) */}
            <path d="M 58 40 L 40 40 A 23 23 0 0 0 17 63 A 23 23 0 0 0 40 86 L 56 86" />

            {/* 2. Cloud Body + Bottom Right Base (Starts after slit, traverses right, arcs up through cloud lobes, curves to top dome with gap) */}
            <path d="M 68 86 L 118 86 C 134 86 142 75 142 63 C 142 51 133 43 126 42 C 126 33 118 24 104 24 C 95 24 88 28 84 32 C 78 17 66 17 58 26" />

            {/* 3. Concentric Center Circle / Core inside left capsule */}
            <circle
              cx="40"
              cy="63"
              r="8"
              stroke="#FFD200"
              strokeWidth="7"
              fill="none"
            />
          </g>
        </svg>
      </div>

      {showWordmark && (
        <div className="flex flex-col">
          <div className="flex items-center gap-1.5 leading-none">
            <span className={`text-[19px] font-bold tracking-tight ${wordmarkColor}`}>
              felcloud
            </span>
            <span className="text-[9px] font-mono font-semibold px-1.5 py-0.5 rounded bg-[#FFD200]/15 text-[#FFD200] border border-[#FFD200]/30 tracking-wider uppercase">
              CSTAM
            </span>
          </div>
          <span className="text-[10px] text-purple-300/70 font-mono tracking-widest uppercase mt-0.5">
            Cloud Control Plane
          </span>
        </div>
      )}
    </div>
  )
}
