import { useCallback } from 'react'
import { fetchGatewayStatus, fetchSandboxes, fetchEvents, type GatewayStatus, type Sandbox, type EventEntry } from '../api'
import { usePolling } from '../hooks'

function StatCard({ label, value, sub, color }: { label: string; value: string | number; sub?: string; color: string }) {
  return (
    <div className="rounded-xl p-5 border transition-all duration-200 hover:scale-[1.02]" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
      <p className="text-xs font-medium text-[var(--color-text-muted)] uppercase tracking-wider">{label}</p>
      <p className="text-3xl font-bold mt-1" style={{ color }}>{value}</p>
      {sub && <p className="text-xs text-[var(--color-text-secondary)] mt-1">{sub}</p>}
    </div>
  )
}

export default function Overview() {
  const gwFetcher = useCallback(() => fetchGatewayStatus(), [])
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const evFetcher = useCallback(() => fetchEvents(10), [])

  const { data: gw } = usePolling<GatewayStatus>(gwFetcher, 5000)
  const { data: sandboxes } = usePolling<Sandbox[]>(sbFetcher, 10000)
  const { data: evData } = usePolling<{ events: EventEntry[] }>(evFetcher, 8000)

  const activeCount = sandboxes?.filter(s => s.status === 'ACTIVE').length ?? 0
  const totalCount = sandboxes?.length ?? 0
  const holder = gw?.vip_holder ?? '—'
  const gwEntries = gw ? Object.entries(gw.gateways) : []

  return (
    <div className="space-y-6">
      {/* Stats grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Active Sandboxes" value={activeCount} sub={`of ${totalCount} total`} color="var(--color-success)" />
        <StatCard label="VIP Holder" value={holder.toUpperCase()} sub={gw?.vip ?? ''} color="var(--color-accent)" />
        <StatCard label="Gateways Online" value={gwEntries.filter(([, g]) => g.reachable).length} sub={`of ${gwEntries.length}`} color="var(--color-cyan)" />
        <StatCard label="Latest Generation" value={gw?.latest_generation ?? '—'} color="var(--color-purple)" />
      </div>

      {/* Two column grid: Gateways + Recent Events */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Gateway status cards */}
        <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
          <h3 className="text-sm font-semibold text-white mb-4">Gateway Health</h3>
          <div className="space-y-3">
            {gwEntries.map(([name, info]) => (
              <div key={name} className="flex items-center justify-between p-3 rounded-lg" style={{ background: 'var(--color-bg-secondary)' }}>
                <div className="flex items-center gap-3">
                  <div className={`w-2.5 h-2.5 rounded-full ${info.reachable ? 'bg-[var(--color-success)]' : 'bg-[var(--color-danger)]'}`}
                    style={info.reachable ? { boxShadow: '0 0 8px rgba(16,185,129,0.5)' } : {}}
                  />
                  <span className="font-mono font-semibold text-white">{name.toUpperCase()}</span>
                </div>
                <div className="flex items-center gap-3 text-xs">
                  <span className="px-2 py-0.5 rounded-md font-mono" style={{
                    background: info.keepalived === 'MASTER' ? 'var(--color-success-muted)' : 'var(--color-bg-input)',
                    color: info.keepalived === 'MASTER' ? 'var(--color-success)' : 'var(--color-text-secondary)',
                  }}>
                    {info.keepalived}
                  </span>
                  <span className="px-2 py-0.5 rounded-md font-mono" style={{
                    background: info.haproxy === 'UP' ? 'var(--color-success-muted)' : 'var(--color-danger-muted)',
                    color: info.haproxy === 'UP' ? 'var(--color-success)' : 'var(--color-danger)',
                  }}>
                    HAProxy: {info.haproxy}
                  </span>
                  {info.vip_holder_local && (
                    <span className="px-2 py-0.5 rounded-md font-mono text-[var(--color-accent)]" style={{ background: 'rgba(99,102,241,0.15)' }}>
                      VIP ●
                    </span>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Recent events */}
        <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
          <h3 className="text-sm font-semibold text-white mb-4">Recent Events</h3>
          <div className="space-y-2 max-h-64 overflow-y-auto">
            {(evData?.events ?? []).slice(0, 8).map((ev, i) => (
              <div key={ev.id ?? i} className="flex items-center justify-between p-2.5 rounded-lg text-xs" style={{ background: 'var(--color-bg-secondary)' }}>
                <div className="flex items-center gap-2 flex-1 min-w-0">
                  <span className="px-1.5 py-0.5 rounded text-[10px] font-bold uppercase shrink-0" style={{
                    background: ev.event_type === 'failover' ? 'var(--color-warning-muted)' : ev.event_type === 'sync' ? 'var(--color-info-muted)' : 'var(--color-bg-input)',
                    color: ev.event_type === 'failover' ? 'var(--color-warning)' : ev.event_type === 'sync' ? 'var(--color-info)' : 'var(--color-text-secondary)',
                  }}>
                    {ev.event_type}
                  </span>
                  <span className="truncate text-[var(--color-text-secondary)]">{ev.detail}</span>
                </div>
                {ev.duration_ms > 0 && (
                  <span className="text-[var(--color-text-muted)] ml-2 shrink-0">{ev.duration_ms}ms</span>
                )}
              </div>
            ))}
            {(!evData?.events || evData.events.length === 0) && (
              <p className="text-xs text-[var(--color-text-muted)] text-center py-4">No events recorded yet</p>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
