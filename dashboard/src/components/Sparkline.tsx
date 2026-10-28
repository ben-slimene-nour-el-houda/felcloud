export interface SparklineProps {
  data: number[]
  color?: 'emerald' | 'amber' | 'rose' | 'purple' | 'blue'
  width?: number
  height?: number
  strokeWidth?: number
  filled?: boolean
  className?: string
}

const colorMaps = {
  emerald: {
    stroke: '#10b981',
    fillStart: 'rgba(16, 185, 129, 0.28)',
    fillEnd: 'rgba(16, 185, 129, 0.0)',
  },
  amber: {
    stroke: '#f59e0b',
    fillStart: 'rgba(245, 158, 11, 0.28)',
    fillEnd: 'rgba(245, 158, 11, 0.0)',
  },
  rose: {
    stroke: '#f43f5e',
    fillStart: 'rgba(244, 63, 94, 0.28)',
    fillEnd: 'rgba(244, 63, 94, 0.0)',
  },
  purple: {
    stroke: '#7e4c83',
    fillStart: 'rgba(126, 76, 131, 0.28)',
    fillEnd: 'rgba(126, 76, 131, 0.0)',
  },
  blue: {
    stroke: '#3b82f6',
    fillStart: 'rgba(59, 130, 246, 0.28)',
    fillEnd: 'rgba(59, 130, 246, 0.0)',
  },
}

export function Sparkline({
  data = [12, 14, 18, 15, 20, 22, 19, 24],
  color = 'emerald',
  width = 96,
  height = 28,
  strokeWidth = 2,
  filled = true,
  className = '',
}: SparklineProps) {
  if (!data || data.length < 2) return null

  const min = Math.min(...data)
  const max = Math.max(...data)
  const range = max - min || 1
  const padding = 2

  const effectiveHeight = height - padding * 2
  const effectiveWidth = width - padding * 2

  const points = data.map((val, index) => {
    const x = padding + (index / (data.length - 1)) * effectiveWidth
    const y = height - padding - ((val - min) / range) * effectiveHeight
    return { x, y }
  })

  const pathD = points.reduce((acc, p, i) => {
    if (i === 0) return `M ${p.x} ${p.y}`
    return `${acc} L ${p.x} ${p.y}`
  }, '')

  const first = points[0]
  const last = points[points.length - 1]
  const areaD = `${pathD} L ${last.x} ${height} L ${first.x} ${height} Z`

  const c = colorMaps[color] || colorMaps.emerald
  const gradId = `spark-grad-${color}-${Math.random().toString(36).substr(2, 6)}`

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={`overflow-visible ${className}`}
    >
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={c.fillStart} />
          <stop offset="100%" stopColor={c.fillEnd} />
        </linearGradient>
      </defs>
      {filled && <path d={areaD} fill={`url(#${gradId})`} />}
      <path
        d={pathD}
        fill="none"
        stroke={c.stroke}
        strokeWidth={strokeWidth}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle
        cx={last.x}
        cy={last.y}
        r={2.5}
        fill={c.stroke}
        className="animate-pulse"
      />
    </svg>
  )
}
