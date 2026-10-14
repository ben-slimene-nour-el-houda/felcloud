import { useState, useCallback } from 'react'
import { fetchGatewayStatus, triggerFailover, triggerSync, triggerRollback, type GatewayStatus, type FailoverResponse, ApiError } from '../api'
import { usePolling } from '../hooks'

export default function Gateways() {
  const gwFetcher = useCallback(() => fetchGatewayStatus(), [])
  const { data: gw, refresh } = usePolling<GatewayStatus>(gwFetcher, 5000)
  const [action, setAction] = useState<string | null>(null)
  const [result, setResult] = useState<{ text: string; ok: boolean } | null>(null)
  const [failoverResult, setFailoverResult] = useState<FailoverResponse | null>(null)

  async function handleFailover(target?: string) {
    setAction('failover'); setResult(null); setFailoverResult(null)
    try {
      const res = await triggerFailover(target)
      setFailoverResult(res)
      setResult({ text: `Failover: ${res.before} → ${res.after} (${res.failover_ms}ms)`, ok: res.success })
      refresh()
    } catch (e: any) {
      setResult({ text: e instanceof ApiError ? e.message : 'Failover failed', ok: false })
    } finally { setAction(null) }
  }

  async function handleSync() {
    setAction('sync'); setResult(null); setFailoverResult(null)
    try {
      const res = await triggerSync()
      setResult({ text: res.success ? `Synced in ${res.duration_ms}ms` : res.message, ok: res.success })
      refresh()
    } catch (e: any) {
      setResult({ text: e instanceof ApiError ? e.message : 'Sync failed', ok: false })
    } finally { setAction(null) }
  }

  async function handleRollback() {
    setAction('rollback'); setResult(null); setFailoverResult(null)
    try {
      const res = await triggerRollback()
      setResult({ text: res.success ? `Rolled back to ${res.generation}` : 'Rollback failed', ok: res.success })
      refresh()
    } catch (e: any) {
      setResult({ text: e instanceof ApiError ? e.message : 'Rollback failed', ok: false })
    } finally { setAction(null) }
  }

  const entries = gw ? Object.entries(gw.gateways) : []

  return (
    <div className="space-y-6">
      {/* Gateway cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        {entries.map(([name, info]) => {
          const isMaster = info.keepalived === 'MASTER'
          return (
            <div key={name} className="rounded-xl border p-6 transition-all duration-300 hover:border-[var(--color-accent)]/30" style={{
              background: 'var(--color-bg-card)',
              borderColor: isMaster ? 'rgba(99,102,241,0.3)' : 'var(--color-border)',
            }}>
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div className={`w-3 h-3 rounded-full ${info.reachable ? 'bg-[var(--color-success)]' : 'bg-[var(--color-danger)]'}`}
                    style={info.reachable ? { boxShadow: '0 0 12px rgba(16,185,129,0.5)' } : { boxShadow: '0 0 12px rgba(239,68,68,0.5)' }}
                  />
                  <h3 className="text-xl font-bold text-white font-mono">{name.toUpperCase()}</h3>
                </div>
                {info.vip_holder_local && (
                  <span className="px-3 py-1 rounded-full text-xs font-bold animate-pulse-glow" style={{
                    background: 'rgba(99,102,241,0.2)',
                    color: 'var(--color-accent)',
                    border: '1px solid rgba(99,102,241,0.3)',
                  }}>
                    ● VIP HOLDER
                  </span>
                )}
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div className="p-3 rounded-lg" style={{ background: 'var(--color-bg-secondary)' }}>
                  <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">Keepalived</span>
                  <p className="text-sm font-bold mt-0.5" style={{ color: isMaster ? 'var(--color-success)' : 'var(--color-warning)' }}>
                    {info.keepalived}
                  </p>
                </div>
                <div className="p-3 rounded-lg" style={{ background: 'var(--color-bg-secondary)' }}>
                  <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">HAProxy</span>
                  <p className="text-sm font-bold mt-0.5" style={{ color: info.haproxy === 'UP' ? 'var(--color-success)' : 'var(--color-danger)' }}>
                    {info.haproxy}
                  </p>
                </div>
                <div className="p-3 rounded-lg col-span-2" style={{ background: 'var(--color-bg-secondary)' }}>
                  <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">Probed At</span>
                  <p className="text-xs font-mono text-[var(--color-text-secondary)] mt-0.5">
                    {info.probed_at ? new Date(info.probed_at).toLocaleTimeString() : '—'}
                  </p>
                </div>
              </div>

              {info.error && (
                <div className="mt-3 p-2 rounded-lg text-xs text-red-400" style={{ background: 'var(--color-danger-muted)' }}>
                  {info.error}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* Actions */}
      <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <h3 className="text-sm font-semibold text-white mb-4">Lifecycle Controls</h3>
        <div className="flex flex-wrap gap-3">
          <button
            id="failover-btn"
            onClick={() => handleFailover()}
            disabled={!!action}
            className="px-5 py-2.5 rounded-xl text-sm font-semibold text-white transition-all hover:scale-[1.02] disabled:opacity-50"
            style={{ background: 'linear-gradient(135deg, #f59e0b, #ef4444)' }}
          >
            {action === 'failover' ? '⟳ Failing over…' : '⇄ Manual Failover'}
          </button>
          <button
            id="sync-btn"
            onClick={handleSync}
            disabled={!!action}
            className="px-5 py-2.5 rounded-xl text-sm font-semibold text-white transition-all hover:scale-[1.02] disabled:opacity-50"
            style={{ background: 'linear-gradient(135deg, #3b82f6, #6366f1)' }}
          >
            {action === 'sync' ? '⟳ Syncing…' : '⟳ Sync Config'}
          </button>
          <button
            id="rollback-btn"
            onClick={handleRollback}
            disabled={!!action}
            className="px-5 py-2.5 rounded-xl text-sm font-semibold text-white transition-all hover:scale-[1.02] disabled:opacity-50"
            style={{ background: 'linear-gradient(135deg, #a855f7, #6366f1)' }}
          >
            {action === 'rollback' ? '⟳ Rolling back…' : '↶ Config Rollback'}
          </button>
          <button
            onClick={() => { fetchGatewayStatus(true).then(() => refresh()) }}
            className="px-5 py-2.5 rounded-xl text-sm font-medium text-[var(--color-text-secondary)] border transition-all hover:bg-white/5"
            style={{ borderColor: 'var(--color-border)' }}
          >
            ↻ Force Refresh
          </button>
        </div>

        {/* Result */}
        {result && (
          <div className={`mt-4 p-3 rounded-lg text-sm ${result.ok ? 'text-green-400 bg-green-400/10' : 'text-red-400 bg-red-400/10'}`}>
            {result.text}
          </div>
        )}

        {/* Failover details */}
        {failoverResult && (
          <div className="mt-4 grid grid-cols-2 sm:grid-cols-4 gap-3">
            {[
              { label: 'Downtime', value: `${failoverResult.downtime_ms}ms` },
              { label: 'Duration', value: `${failoverResult.failover_ms}ms` },
              { label: 'Failed Requests', value: failoverResult.failed_requests },
              { label: 'Verified By', value: failoverResult.verified_by },
            ].map(item => (
              <div key={item.label} className="p-3 rounded-lg" style={{ background: 'var(--color-bg-secondary)' }}>
                <span className="text-[10px] uppercase tracking-wider text-[var(--color-text-muted)]">{item.label}</span>
                <p className="text-sm font-mono font-semibold text-white mt-0.5">{item.value}</p>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* VIP info */}
      {gw && (
        <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
          <h3 className="text-sm font-semibold text-white mb-3">Cluster Info</h3>
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-4 text-sm">
            <div>
              <span className="text-[var(--color-text-muted)]">VIP Address</span>
              <p className="font-mono text-white">{gw.vip}</p>
            </div>
            <div>
              <span className="text-[var(--color-text-muted)]">Current Holder</span>
              <p className="font-mono text-[var(--color-accent)] font-bold">{gw.vip_holder?.toUpperCase()}</p>
            </div>
            <div>
              <span className="text-[var(--color-text-muted)]">Generation</span>
              <p className="font-mono text-white">{gw.latest_generation}</p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
