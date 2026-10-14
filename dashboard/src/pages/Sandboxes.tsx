import { useState, useCallback } from 'react'
import { fetchSandboxes, provisionSandbox, teardownSandbox, type Sandbox, ApiError } from '../api'
import { usePolling, useCountdown, formatDuration, isValidDnsLabel } from '../hooks'

function SandboxRow({ sb, onTeardown }: { sb: Sandbox; onTeardown: (id: string) => void }) {
  const remaining = useCountdown(sb.expires_at)
  const [tearing, setTearing] = useState(false)

  const statusColor: Record<string, string> = {
    ACTIVE: 'var(--color-success)',
    PROVISIONING: 'var(--color-warning)',
    TEARING_DOWN: 'var(--color-danger)',
    RELEASED: 'var(--color-text-muted)',
    QUARANTINE: 'var(--color-purple)',
  }

  async function handleTeardown() {
    if (!confirm(`Teardown sandbox "${sb.sandbox_id}"?`)) return
    setTearing(true)
    try { await onTeardown(sb.sandbox_id) } finally { setTearing(false) }
  }

  return (
    <tr className="border-b transition-colors hover:bg-white/[0.02]" style={{ borderColor: 'var(--color-border)' }}>
      <td className="py-3 px-4">
        <span className="font-mono font-semibold text-white">{sb.sandbox_id}</span>
      </td>
      <td className="py-3 px-4 font-mono text-sm text-[var(--color-text-secondary)]">{sb.ip_address}</td>
      <td className="py-3 px-4">
        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-xs font-bold" style={{
          background: `${statusColor[sb.status] ?? 'var(--color-text-muted)'}20`,
          color: statusColor[sb.status] ?? 'var(--color-text-muted)',
        }}>
          <span className="w-1.5 h-1.5 rounded-full" style={{ background: statusColor[sb.status] ?? 'var(--color-text-muted)' }} />
          {sb.status}
        </span>
      </td>
      <td className="py-3 px-4 text-sm text-[var(--color-text-secondary)]">
        {sb.hostname || '—'}
      </td>
      <td className="py-3 px-4 font-mono text-sm">
        {sb.status === 'ACTIVE' && sb.expires_at ? (
          <span style={{ color: remaining < 300 ? 'var(--color-danger)' : remaining < 900 ? 'var(--color-warning)' : 'var(--color-text-secondary)' }}>
            {formatDuration(remaining)}
          </span>
        ) : '—'}
      </td>
      <td className="py-3 px-4">
        {sb.status === 'ACTIVE' && (
          <button
            onClick={handleTeardown}
            disabled={tearing}
            className="px-3 py-1.5 rounded-lg text-xs font-medium text-red-400 transition-colors hover:bg-red-400/10 disabled:opacity-50"
          >
            {tearing ? 'Tearing…' : 'Teardown'}
          </button>
        )}
      </td>
    </tr>
  )
}

export default function Sandboxes() {
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const { data: sandboxes, refresh } = usePolling<Sandbox[]>(sbFetcher, 8000)
  const [team, setTeam] = useState('')
  const [provisioning, setProvisioning] = useState(false)
  const [msg, setMsg] = useState<{ text: string; ok: boolean } | null>(null)

  async function handleProvision() {
    if (!team.trim() || !isValidDnsLabel(team.trim())) {
      setMsg({ text: 'Invalid team name (must be a lowercase DNS label)', ok: false })
      return
    }
    setProvisioning(true); setMsg(null)
    try {
      await provisionSandbox(team.trim())
      setMsg({ text: `Sandbox "${team}" provisioned`, ok: true })
      setTeam('')
      refresh()
    } catch (e: any) {
      setMsg({ text: e instanceof ApiError ? e.message : 'Provision failed', ok: false })
    } finally {
      setProvisioning(false)
    }
  }

  async function handleTeardown(id: string) {
    try {
      await teardownSandbox(id)
      setMsg({ text: `Sandbox "${id}" torn down`, ok: true })
      refresh()
    } catch (e: any) {
      setMsg({ text: e instanceof ApiError ? e.message : 'Teardown failed', ok: false })
    }
  }

  const list = sandboxes ?? []

  return (
    <div className="space-y-5">
      {/* Provision bar */}
      <div className="flex flex-wrap items-center gap-3 p-4 rounded-xl border" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <input
          id="provision-team-input"
          type="text"
          value={team}
          onChange={e => setTeam(e.target.value.toLowerCase())}
          placeholder="team-name"
          className="px-4 py-2.5 rounded-xl text-sm text-white placeholder-[var(--color-text-muted)] border focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] transition-all"
          style={{ background: 'var(--color-bg-input)', borderColor: 'var(--color-border)', minWidth: 200 }}
        />
        <button
          id="provision-btn"
          onClick={handleProvision}
          disabled={provisioning}
          className="px-5 py-2.5 rounded-xl text-sm font-semibold text-white transition-all hover:scale-[1.02] active:scale-[0.98] disabled:opacity-50"
          style={{ background: 'linear-gradient(135deg, #6366f1, #a855f7)' }}
        >
          {provisioning ? 'Provisioning…' : '+ Spin Up Sandbox'}
        </button>
        {msg && (
          <span className={`text-sm px-3 py-1 rounded-lg ${msg.ok ? 'text-green-400 bg-green-400/10' : 'text-red-400 bg-red-400/10'}`}>
            {msg.text}
          </span>
        )}
      </div>

      {/* Table */}
      <div className="rounded-xl border overflow-hidden" style={{ background: 'var(--color-bg-card)', borderColor: 'var(--color-border)' }}>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-xs uppercase tracking-wider text-[var(--color-text-muted)]" style={{ borderColor: 'var(--color-border)' }}>
                <th className="py-3 px-4 text-left font-medium">Sandbox ID</th>
                <th className="py-3 px-4 text-left font-medium">Private IP</th>
                <th className="py-3 px-4 text-left font-medium">Status</th>
                <th className="py-3 px-4 text-left font-medium">Hostname</th>
                <th className="py-3 px-4 text-left font-medium">TTL</th>
                <th className="py-3 px-4 text-left font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {list.map(sb => (
                <SandboxRow key={sb.sandbox_id} sb={sb} onTeardown={handleTeardown} />
              ))}
              {list.length === 0 && (
                <tr>
                  <td colSpan={6} className="py-12 text-center text-[var(--color-text-muted)]">
                    No sandboxes found. Provision one above.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
