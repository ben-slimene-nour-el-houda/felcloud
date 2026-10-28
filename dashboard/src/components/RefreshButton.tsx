import { useState } from 'react'

interface RefreshButtonProps {
  onRefresh: () => void | Promise<void>
  label?: string
  className?: string
  isRefreshing?: boolean
}

export function RefreshButton({
  onRefresh,
  label = 'Refresh',
  className = '',
  isRefreshing: externalRefreshing,
}: RefreshButtonProps) {
  const [internalRefreshing, setInternalRefreshing] = useState(false)
  const spinning = externalRefreshing !== undefined ? externalRefreshing : internalRefreshing

  async function handleClick() {
    if (spinning) return
    setInternalRefreshing(true)
    try {
      await onRefresh()
    } finally {
      setTimeout(() => setInternalRefreshing(false), 600)
    }
  }

  return (
    <button
      onClick={handleClick}
      disabled={spinning}
      title="Fetch latest telemetry"
      className={`inline-flex items-center gap-1.5 px-3 py-1.5 bg-white/90 hover:bg-white text-[#2D0035] rounded-xl text-xs font-semibold shadow-xs hover:shadow-sm border border-purple-900/5 transition-all duration-200 cursor-pointer disabled:opacity-70 ${className}`}
    >
      <span
        className={`material-symbols-outlined text-[16px] transition-transform duration-700 ease-in-out ${
          spinning ? 'animate-spin text-[#7e4c83]' : 'text-[#827787]'
        }`}
      >
        refresh
      </span>
      {label && <span>{label}</span>}
    </button>
  )
}
