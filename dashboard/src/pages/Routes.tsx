import { useCallback } from 'react'
import { fetchSandboxes, fetchConfig, type Sandbox, type ConfigResponse } from '../api'
import { usePolling } from '../hooks'

export default function Routes() {
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const cfgFetcher = useCallback(() => fetchConfig(), [])
  const { data: sandboxes } = usePolling<Sandbox[]>(sbFetcher, 10000)
  const { data: config } = usePolling<ConfigResponse>(cfgFetcher, 60000)

  const domain = config?.routing_domain ?? 'cstam.felcloud.tn'
  const activeSandboxes = (sandboxes ?? []).filter(s => s.status === 'ACTIVE')

  return (
    <div className="space-y-5">
      {/* Domain info */}
      <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-semibold text-white">Routing Domain</h3>
            <p className="text-2xl font-mono font-bold text-[var(--color-accent)] mt-1">{domain}</p>
          </div>
          <div className="text-right">
            <span className="text-xs text-[var(--color-text-muted)]">VIP</span>
            <p className="font-mono text-white">{config?.vip_address ?? '—'}</p>
          </div>
        </div>
      </div>

      {/* Subdomain mapping table */}
      <div className="rounded-xl border overflow-hidden" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <div className="px-5 py-3 border-b" style={{ borderColor: 'var(--color-border)' }}>
          <h3 className="text-sm font-semibold text-white">Mapped Subdomains</h3>
          <p className="text-xs text-[var(--color-text-muted)] mt-0.5">{activeSandboxes.length} active route{activeSandboxes.length !== 1 ? 's' : ''}</p>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-xs uppercase tracking-wider text-[var(--color-text-muted)]" style={{ borderColor: 'var(--color-border)' }}>
                <th className="py-3 px-5 text-left font-medium">Subdomain</th>
                <th className="py-3 px-5 text-left font-medium">Full URL</th>
                <th className="py-3 px-5 text-left font-medium">Backend IP</th>
                <th className="py-3 px-5 text-left font-medium">Custom Domain</th>
              </tr>
            </thead>
            <tbody>
              {activeSandboxes.map(sb => (
                <tr key={sb.sandbox_id} className="border-b transition-colors hover:bg-white/[0.02]" style={{ borderColor: 'var(--color-border)' }}>
                  <td className="py-3 px-5 font-mono font-semibold text-white">{sb.sandbox_id}</td>
                  <td className="py-3 px-5">
                    <a
                      href={`http://${sb.sandbox_id}.${domain}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] transition-colors"
                    >
                      {sb.sandbox_id}.{domain}
                    </a>
                  </td>
                  <td className="py-3 px-5 font-mono text-[var(--color-text-secondary)]">{sb.ip_address}</td>
                  <td className="py-3 px-5 font-mono text-xs">
                    {sb.custom_domain ? (
                      <span className="text-[var(--color-purple)]">{sb.custom_domain}</span>
                    ) : (
                      <span className="text-[var(--color-text-muted)]">—</span>
                    )}
                  </td>
                </tr>
              ))}
              {activeSandboxes.length === 0 && (
                <tr>
                  <td colSpan={4} className="py-12 text-center text-[var(--color-text-muted)]">
                    No active routes. Provision sandboxes to see mapped subdomains.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Architecture diagram */}
      <div className="rounded-xl border p-5" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <h3 className="text-sm font-semibold text-white mb-4">Traffic Flow</h3>
        <div className="flex items-center justify-center gap-4 py-4 overflow-x-auto text-xs">
          <div className="shrink-0 text-center">
            <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl" style={{ background: 'var(--color-bg-secondary)' }}>🌐</div>
            <span className="text-[var(--color-text-muted)] mt-1 block">Client</span>
          </div>
          <span className="text-[var(--color-text-muted)] text-lg">→</span>
          <div className="shrink-0 text-center">
            <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl" style={{ background: 'var(--color-info-muted)' }}>🔀</div>
            <span className="text-[var(--color-text-muted)] mt-1 block">DNS</span>
            <span className="text-[10px] text-[var(--color-text-muted)]">*.{domain}</span>
          </div>
          <span className="text-[var(--color-text-muted)] text-lg">→</span>
          <div className="shrink-0 text-center">
            <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl animate-pulse-glow" style={{ background: 'rgba(99,102,241,0.2)' }}>⚡</div>
            <span className="text-[var(--color-text-muted)] mt-1 block">VIP</span>
            <span className="text-[10px] text-[var(--color-accent)]">{config?.vip_address}</span>
          </div>
          <span className="text-[var(--color-text-muted)] text-lg">→</span>
          <div className="shrink-0 text-center">
            <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl" style={{ background: 'var(--color-success-muted)' }}>🔄</div>
            <span className="text-[var(--color-text-muted)] mt-1 block">HAProxy</span>
            <span className="text-[10px] text-[var(--color-text-muted)]">GW1/GW2</span>
          </div>
          <span className="text-[var(--color-text-muted)] text-lg">→</span>
          <div className="shrink-0 text-center">
            <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl" style={{ background: 'var(--color-purple)/20' }}>📦</div>
            <span className="text-[var(--color-text-muted)] mt-1 block">Sandbox</span>
            <span className="text-[10px] text-[var(--color-text-muted)]">10.20.10.x</span>
          </div>
        </div>
      </div>
    </div>
  )
}
