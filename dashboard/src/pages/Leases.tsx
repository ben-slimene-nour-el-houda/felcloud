import { useCallback } from 'react'
import { fetchLeases, type LeaseEntry } from '../api'
import { usePolling, useCountdown, formatDuration } from '../hooks'

function LeaseRow({ lease }: { lease: LeaseEntry }) {
  const remaining = useCountdown(lease.expires_at)
  const statusColor: Record<string, string> = {
    ACTIVE: 'var(--color-success)',
    RELEASED: 'var(--color-text-muted)',
    QUARANTINE: 'var(--color-warning)',
    EXPIRED: 'var(--color-danger)',
  }

  return (
    <tr className="border-b transition-colors hover:bg-white/[0.02]" style={{ borderColor: 'var(--color-border)' }}>
      <td className="py-3 px-4 font-mono font-semibold text-white text-sm">{lease.sandbox_id}</td>
      <td className="py-3 px-4 font-mono text-sm text-[var(--color-text-secondary)]">{lease.ip_address}</td>
      <td className="py-3 px-4">
        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-xs font-bold" style={{
          background: `${statusColor[lease.status] ?? 'var(--color-text-muted)'}20`,
          color: statusColor[lease.status] ?? 'var(--color-text-muted)',
        }}>
          <span className="w-1.5 h-1.5 rounded-full" style={{ background: statusColor[lease.status] }} />
          {lease.status}
        </span>
      </td>
      <td className="py-3 px-4 text-xs text-[var(--color-text-secondary)]">
        {lease.created_at ? new Date(lease.created_at).toLocaleString() : '—'}
      </td>
      <td className="py-3 px-4 font-mono text-sm">
        {lease.status === 'ACTIVE' && lease.expires_at ? (
          <span style={{ color: remaining < 300 ? 'var(--color-danger)' : 'var(--color-text-secondary)' }}>
            {formatDuration(remaining)}
          </span>
        ) : '—'}
      </td>
      <td className="py-3 px-4 text-xs text-[var(--color-text-muted)]">
        {lease.released_at ? new Date(lease.released_at).toLocaleString() : '—'}
      </td>
    </tr>
  )
}

export default function Leases() {
  const leaseFetcher = useCallback(() => fetchLeases(), [])
  const { data: leases, loading } = usePolling<LeaseEntry[]>(leaseFetcher, 10000)
  const list = leases ?? []

  const activeCount = list.filter(l => l.status === 'ACTIVE').length
  const releasedCount = list.filter(l => l.status === 'RELEASED').length
  const quarantineCount = list.filter(l => l.status === 'QUARANTINE').length

  return (
    <div className="space-y-5">
      {/* Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
        {[
          { label: 'Total Leases', value: list.length, color: 'var(--color-text-primary)' },
          { label: 'Active', value: activeCount, color: 'var(--color-success)' },
          { label: 'Released', value: releasedCount, color: 'var(--color-text-muted)' },
          { label: 'Quarantine', value: quarantineCount, color: 'var(--color-warning)' },
        ].map(s => (
          <div key={s.label} className="rounded-xl p-4 border" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
            <p className="text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">{s.label}</p>
            <p className="text-2xl font-bold mt-1" style={{ color: s.color }}>{s.value}</p>
          </div>
        ))}
      </div>

      {/* Table */}
      <div className="rounded-xl border overflow-hidden" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        {loading && list.length === 0 ? (
          <div className="p-12 text-center text-[var(--color-text-muted)]">Loading leases…</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-xs uppercase tracking-wider text-[var(--color-text-muted)]" style={{ borderColor: 'var(--color-border)' }}>
                  <th className="py-3 px-4 text-left font-medium">Sandbox ID</th>
                  <th className="py-3 px-4 text-left font-medium">IP Address</th>
                  <th className="py-3 px-4 text-left font-medium">Status</th>
                  <th className="py-3 px-4 text-left font-medium">Created</th>
                  <th className="py-3 px-4 text-left font-medium">TTL</th>
                  <th className="py-3 px-4 text-left font-medium">Released</th>
                </tr>
              </thead>
              <tbody>
                {list.map(l => <LeaseRow key={`${l.sandbox_id}-${l.ip_address}`} lease={l} />)}
                {list.length === 0 && (
                  <tr>
                    <td colSpan={6} className="py-12 text-center text-[var(--color-text-muted)]">No leases</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
