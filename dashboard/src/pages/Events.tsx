import { useCallback } from 'react'
import { fetchEvents, type EventEntry } from '../api'
import { usePolling } from '../hooks'

const TYPE_COLORS: Record<string, { bg: string; fg: string }> = {
  failover:         { bg: 'var(--color-warning-muted)', fg: 'var(--color-warning)' },
  rollback:         { bg: 'var(--color-purple)/20',     fg: 'var(--color-purple)' },
  sync:             { bg: 'var(--color-info-muted)',    fg: 'var(--color-info)' },
  keepalived_state: { bg: 'var(--color-success-muted)', fg: 'var(--color-success)' },
  failover_test:    { bg: 'var(--color-cyan)/20',       fg: 'var(--color-cyan)' },
  health_check:     { bg: 'var(--color-success-muted)', fg: 'var(--color-success)' },
}

export default function Events() {
  const evFetcher = useCallback(() => fetchEvents(200), [])
  const { data: evData, loading, refresh } = usePolling<{ events: EventEntry[] }>(evFetcher, 6000)
  const events = evData?.events ?? []

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <p className="text-xs text-[var(--color-text-muted)]">{events.length} events loaded</p>
        </div>
        <button
          onClick={refresh}
          className="px-4 py-2 rounded-lg text-xs font-medium text-[var(--color-text-secondary)] border transition-all hover:bg-white/5"
          style={{ borderColor: 'var(--color-border)' }}
        >
          ↻ Refresh
        </button>
      </div>

      {/* Timeline */}
      <div className="rounded-xl border" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        {loading && events.length === 0 ? (
          <div className="p-12 text-center text-[var(--color-text-muted)]">Loading events…</div>
        ) : events.length === 0 ? (
          <div className="p-12 text-center text-[var(--color-text-muted)]">No events recorded yet</div>
        ) : (
          <div className="divide-y" style={{ borderColor: 'var(--color-border)' }}>
            {events.map((ev, i) => {
              const colors = TYPE_COLORS[ev.event_type] ?? { bg: 'var(--color-bg-input)', fg: 'var(--color-text-secondary)' }
              return (
                <div key={ev.id ?? i} className="flex items-start gap-4 px-5 py-4 transition-colors hover:bg-white/[0.02]">
                  {/* Timeline dot */}
                  <div className="flex flex-col items-center pt-1 shrink-0">
                    <div className="w-2.5 h-2.5 rounded-full" style={{ background: colors.fg }} />
                    {i < events.length - 1 && <div className="w-px flex-1 mt-1" style={{ background: 'var(--color-border)' }} />}
                  </div>

                  {/* Content */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="px-2 py-0.5 rounded text-[10px] font-bold uppercase shrink-0" style={{ background: colors.bg, color: colors.fg }}>
                        {ev.event_type}
                      </span>
                      <span className="text-xs text-[var(--color-text-muted)] font-mono">{ev.scope}</span>
                      {ev.duration_ms > 0 && (
                        <span className="text-xs text-[var(--color-text-muted)]">{ev.duration_ms}ms</span>
                      )}
                    </div>
                    <p className="text-sm text-[var(--color-text-secondary)] mt-1 truncate">{ev.detail}</p>
                  </div>

                  {/* Timestamp */}
                  <span className="text-xs text-[var(--color-text-muted)] shrink-0 font-mono">
                    {ev.created_at ? new Date(ev.created_at).toLocaleString() : '—'}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
