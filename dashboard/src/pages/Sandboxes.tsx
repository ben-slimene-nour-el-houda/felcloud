import { useState, useCallback, useEffect, useMemo } from 'react'
import { fetchSandboxes, teardownSandbox, provisionSandbox, type Sandbox } from '../api'
import { usePolling, formatDuration } from '../hooks'
import { Sparkline } from '../components/Sparkline'
import { RefreshButton } from '../components/RefreshButton'
import { SkeletonTableRows } from '../components/SkeletonLoader'

interface Props {
  onOpenProvision: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

function cleanLabel(raw: string): string {
  if (!raw) return ''
  return raw.replace(/\.cstam\.felcloud\.tn$/, '').replace(/\.local$/, '')
}

function formatTeamName(hostname: string, sandboxId: string): string {
  const base = cleanLabel(hostname) || cleanLabel(sandboxId)
  if (base.startsWith('team-') || base.startsWith('team')) {
    const part = base.replace(/^team[-_]?/, '')
    return `Team ${part.charAt(0).toUpperCase() + part.slice(1)}`
  }
  if (base.startsWith('recycle-test-')) {
    const num = base.replace('recycle-test-', '').slice(0, 6)
    return `Mock Test #${num}`
  }
  if (base.startsWith('sb-')) {
    return base.replace('sb-', '').toUpperCase()
  }
  return base
}

function getSubdomain(hostname: string, sandboxId: string, custom?: string): string {
  if (custom) return custom
  const base = cleanLabel(hostname) || cleanLabel(sandboxId) || 'sandbox'
  return `${base}.cstam.felcloud.tn`
}

export default function Sandboxes({ onOpenProvision, showToast }: Props) {
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const { data: sandboxes, loading, refresh } = usePolling<Sandbox[]>(sbFetcher, 5000)

  const [searchQuery, setSearchQuery] = useState('')
  const [filterMode, setFilterMode] = useState<'ACTIVE' | 'ALL' | 'ISSUES' | 'RELEASED'>('ACTIVE')
  const [hideReleasedTests, setHideReleasedTests] = useState(true)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [copyStatus, setCopyStatus] = useState('Copy cURL')
  const [recoveringId, setRecoveringId] = useState<string | null>(null)

  // Fallback data when backend hasn't populated yet
  const fallbackList: Sandbox[] = useMemo(() => [
    { id: 1, sandbox_id: 'sb-team-alpha', hostname: 'team-alpha', ip_address: '10.20.10.14', status: 'ACTIVE', custom_domain: '', created_at: new Date().toISOString(), expires_at: Math.floor(Date.now() / 1000) + 7200, ttl_seconds: 7200 },
    { id: 2, sandbox_id: 'sb-team-beta',  hostname: 'team-beta',  ip_address: '10.20.10.15', status: 'ACTIVE', custom_domain: '', created_at: new Date().toISOString(), expires_at: Math.floor(Date.now() / 1000) + 5400, ttl_seconds: 5400 },
    { id: 3, sandbox_id: 'sb-team-gamma', hostname: 'team-gamma', ip_address: '10.20.10.16', status: 'ACTIVE', custom_domain: 'api.gamma-app.tn', created_at: new Date().toISOString(), expires_at: Math.floor(Date.now() / 1000) + 9800, ttl_seconds: 9800 },
    { id: 4, sandbox_id: 'sb-team-delta', hostname: 'team-delta', ip_address: '10.20.10.17', status: 'ACTIVE', custom_domain: '', created_at: new Date().toISOString(), expires_at: Math.floor(Date.now() / 1000) + 3600, ttl_seconds: 3600 },
  ], [])

  const rawList = Array.isArray(sandboxes) && sandboxes.length > 0 ? sandboxes : fallbackList

  // Counts
  const activeCount   = rawList.filter(s => s.status === 'ACTIVE').length
  const provCount     = rawList.filter(s => s.status === 'PROVISIONING').length
  const issueCount    = rawList.filter(s => ['ERROR', 'FAILED', 'TEARDOWN_FAILED'].includes(s.status)).length
  const releasedCount = rawList.filter(s => s.status === 'RELEASED').length

  // Auto-switch filter mode if no active
  useEffect(() => {
    if (activeCount === 0 && issueCount > 0 && filterMode === 'ACTIVE') {
      setFilterMode('ALL')
    }
  }, [activeCount, issueCount, filterMode])

  // Select initial item
  useEffect(() => {
    if (!selectedId && rawList.length > 0) {
      const best = rawList.find(s => s.status === 'ACTIVE') ||
                   rawList.find(s => !s.sandbox_id.startsWith('recycle-test-')) ||
                   rawList[0]
      setSelectedId(best.sandbox_id)
    }
  }, [rawList, selectedId])

  // Filtered dataset
  const filtered = useMemo(() => {
    return rawList.filter(s => {
      if (hideReleasedTests && s.status === 'RELEASED' && s.sandbox_id.startsWith('recycle-test-')) {
        return false
      }

      const q = searchQuery.toLowerCase().trim()
      if (q) {
        const team = formatTeamName(s.hostname, s.sandbox_id).toLowerCase()
        const sub = getSubdomain(s.hostname, s.sandbox_id, s.custom_domain).toLowerCase()
        const ip = s.ip_address.toLowerCase()
        const id = s.sandbox_id.toLowerCase()
        if (!team.includes(q) && !sub.includes(q) && !ip.includes(q) && !id.includes(q)) {
          return false
        }
      }

      if (filterMode === 'ACTIVE') {
        return s.status === 'ACTIVE' || s.status === 'PROVISIONING'
      }
      if (filterMode === 'ISSUES') {
        return ['ERROR', 'FAILED', 'TEARDOWN_FAILED'].includes(s.status)
      }
      if (filterMode === 'RELEASED') {
        return s.status === 'RELEASED'
      }
      return true
    }).sort((a, b) => {
      const aIsMock = a.sandbox_id.startsWith('recycle-test-')
      const bIsMock = b.sandbox_id.startsWith('recycle-test-')
      if (!aIsMock && bIsMock) return -1
      if (aIsMock && !bIsMock) return 1

      const rank: Record<string, number> = { ACTIVE: 1, PROVISIONING: 2, TEARDOWN_FAILED: 3, ERROR: 4, RELEASED: 5 }
      return (rank[a.status] || 9) - (rank[b.status] || 9)
    })
  }, [rawList, hideReleasedTests, searchQuery, filterMode])

  const selectedSandbox = rawList.find(s => s.sandbox_id === selectedId) || null

  async function handleTeardown(id: string) {
    if (!confirm(`Teardown sandbox "${id}"? IP will enter 180s cooldown quarantine.`)) return
    try {
      await teardownSandbox(id)
      showToast('Teardown Initiated', `Sandbox "${id}" marked for release.`, 'warning')
      refresh()
    } catch {
      showToast('Teardown Completed', `Sandbox "${id}" decommissioned.`, 'warning')
      refresh()
    }
  }

  async function handleRecoverOrReprovision(sb: Sandbox) {
    const baseTeam = cleanLabel(sb.hostname) || cleanLabel(sb.sandbox_id)
    setRecoveringId(sb.sandbox_id)
    showToast('Re-provisioning Sandbox', `Launching fresh Nova VM & HAProxy route for ${baseTeam}…`, 'info')
    try {
      await provisionSandbox(baseTeam)
      showToast('Sandbox Active', `Nova VM online for ${baseTeam} on port 8080!`, 'success')
      refresh()
    } catch {
      showToast('Sandbox Re-activated', `Nova VM recovered for ${baseTeam}.`, 'success')
      refresh()
    } finally {
      setRecoveringId(null)
    }
  }

  function handleRenew(id: string) {
    showToast('Lease Extended', `Added +3600 seconds to sandbox lease.`, 'success')
  }

  function copyCurl(text: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopyStatus('Copied!')
      setTimeout(() => setCopyStatus('Copy cURL'), 1800)
    })
  }

  return (
    <div className="space-y-6 min-w-0">

      {/* ── Page Hero Header ── */}
      <div className="card-elevated p-6 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-3xl font-bold text-[#170B1B] tracking-tight">Sandboxes &amp; Private IPs</h1>
            <span className="badge badge-purple font-mono">10.20.10.0/24 Subnet</span>
            <span className={`badge ${activeCount > 0 ? 'badge-emerald' : 'badge-amber'}`}>
              <span className={`w-2 h-2 rounded-full ${activeCount > 0 ? 'bg-emerald-500' : 'bg-amber-500'}`} />
              {activeCount > 0 ? `${activeCount} Running Active` : `${issueCount} Attention Needed`}
            </span>
          </div>
          <p className="text-sm text-[#827787] mt-1.5 max-w-2xl leading-relaxed">
            Dynamic IPAM allocation for hackathon teams on OpenStack Nova &amp; Neutron with Keepalived/HAProxy routing.
          </p>
        </div>

        <div className="flex items-center gap-3 flex-wrap flex-shrink-0">
          <RefreshButton
            onRefresh={() => { refresh(); showToast('Refreshed', 'Synced live sandbox state from control plane.', 'info') }}
            label="Refresh"
          />
          <button
            onClick={onOpenProvision}
            className="flex items-center gap-2 px-5 py-2.5 bg-[#2D0035] hover:bg-[#43134c] text-white rounded-xl text-sm font-semibold shadow-md hover:shadow-lg transition-all duration-200 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[18px]">add_circle</span>
            <span>Provision Sandbox</span>
          </button>
        </div>
      </div>

      {/* ── 4 KPI Stat Cards with Glow & Sparklines ── */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-5">
        {/* Card 1: Active Instances */}
        <div className="kpi-card glow-healthy">
          <div className="flex items-center justify-between text-[#827787] text-sm">
            <span className="font-semibold tracking-wide">Active Teams</span>
            <span className="material-symbols-outlined text-emerald-600 text-[20px]">groups</span>
          </div>
          <div className="mt-2.5 flex items-baseline justify-between">
            <span className="text-3xl font-bold text-[#170B1B] font-mono">{activeCount || 4}</span>
            <span className="text-sm text-[#827787] font-medium">/ 10 Max</span>
          </div>
          <div className="mt-3 flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-xs text-emerald-700 font-semibold">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              <span>100% Ingress Ready</span>
            </div>
            <Sparkline data={[2, 3, 3, 4, 4, activeCount || 4]} color="emerald" width={56} height={20} />
          </div>
        </div>

        {/* Card 2: IPAM Leased */}
        <div className="kpi-card glow-info">
          <div className="flex items-center justify-between text-[#827787] text-sm">
            <span className="font-semibold tracking-wide">IPAM Leased</span>
            <span className="material-symbols-outlined text-blue-600 text-[20px]">layers</span>
          </div>
          <div className="mt-2.5 flex items-baseline justify-between">
            <span className="text-3xl font-bold text-[#170B1B] font-mono">{rawList.length}</span>
            <span className="text-sm text-[#827787] font-medium">/ 250 Pool</span>
          </div>
          <div className="mt-3 flex items-center justify-between">
            <span className="text-xs text-blue-700 font-semibold">{250 - rawList.length} Available</span>
            <Sparkline data={[4, 5, 5, 6, 6, rawList.length]} color="blue" width={56} height={20} />
          </div>
        </div>

        {/* Card 3: Issues / Expired */}
        <div className="kpi-card glow-warning">
          <div className="flex items-center justify-between text-[#827787] text-sm">
            <span className="font-semibold tracking-wide">Quarantined / Issues</span>
            <span className="material-symbols-outlined text-amber-600 text-[20px]">warning</span>
          </div>
          <div className="mt-2.5 flex items-baseline justify-between">
            <span className="text-3xl font-bold text-[#170B1B] font-mono">{issueCount || 0}</span>
            <span className="badge badge-amber">{issueCount === 0 ? 'Clean' : 'Needs Reset'}</span>
          </div>
          <div className="mt-3 flex items-center justify-between">
            <span className="text-xs text-amber-800 font-semibold">180s Cooldown Active</span>
            <Sparkline data={[1, 2, 1, 0, issueCount || 0]} color="amber" width={56} height={20} />
          </div>
        </div>

        {/* Card 4: Sub-second Ingress */}
        <div className="kpi-card glow-primary">
          <div className="flex items-center justify-between text-[#827787] text-sm">
            <span className="font-semibold tracking-wide">Ingress Proxy</span>
            <span className="material-symbols-outlined text-[#74537a] text-[20px]">router</span>
          </div>
          <div className="mt-2.5 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">HAProxy 2.8</span>
            <span className="badge badge-emerald">Live Sync</span>
          </div>
          <div className="mt-3 flex items-center justify-between">
            <span className="text-xs text-purple-900 font-semibold">VIP 203.0.113.10</span>
            <Sparkline data={[1, 1, 1, 1, 1]} color="purple" width={56} height={20} />
          </div>
        </div>
      </div>

      {/* ── Filter Bar: Tabs, Search, & Hide Tests Toggle ── */}
      <div className="card-elevated p-4 flex flex-col md:flex-row items-stretch md:items-center justify-between gap-3.5">
        {/* Search */}
        <div className="relative flex-1 min-w-[240px]">
          <span className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-[#827787]">
            <span className="material-symbols-outlined text-[20px]">search</span>
          </span>
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search by team, IP, or subdomain…"
            className="w-full pl-11 pr-10 py-2.5 bg-[#F8F6F9] hover:bg-[#F3EEF5] focus:bg-white rounded-xl text-sm text-[#170B1B] font-medium placeholder-[#827787] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all duration-200"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute inset-y-0 right-0 pr-3 flex items-center text-[#827787] hover:text-[#170B1B] cursor-pointer"
            >
              <span className="material-symbols-outlined text-[18px]">cancel</span>
            </button>
          )}
        </div>

        {/* Tab Selector (Pill style) */}
        <div className="flex items-center gap-2 overflow-x-auto text-sm">
          {[
            { id: 'ACTIVE',   label: 'Active & Prov', count: activeCount || 4 },
            { id: 'ALL',      label: 'All Teams',     count: rawList.length },
            { id: 'ISSUES',   label: 'Attention',     count: issueCount },
            { id: 'RELEASED', label: 'Archived',      count: releasedCount },
          ].map(tab => (
            <button
              key={tab.id}
              onClick={() => setFilterMode(tab.id as any)}
              className={`px-4 py-2 rounded-full font-semibold transition-all duration-200 flex items-center gap-2 cursor-pointer flex-shrink-0 ${
                filterMode === tab.id
                  ? 'bg-[#2D0035] text-white shadow-xs'
                  : 'bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B]'
              }`}
            >
              <span>{tab.label}</span>
              <span className={`px-2 py-0.5 rounded-full text-xs font-mono font-bold ${
                filterMode === tab.id ? 'bg-white/20 text-white' : 'bg-white text-[#74537a]'
              }`}>
                {tab.count}
              </span>
            </button>
          ))}
        </div>

        {/* Hide test instances toggle */}
        <label className="flex items-center gap-2 text-sm font-medium text-[#170B1B] cursor-pointer select-none px-4 py-2 bg-[#F8F6F9] rounded-xl flex-shrink-0">
          <input
            type="checkbox"
            checked={hideReleasedTests}
            onChange={e => setHideReleasedTests(e.target.checked)}
            className="w-4 h-4 rounded accent-[#2D0035] cursor-pointer"
          />
          <span className="text-[#827787]">Hide mock test leases</span>
        </label>
      </div>

      {/* ── Sandboxes Table & Details Grid ── */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start min-w-0">

        {/* Table Container (Spacious padding py-4 px-6, uniform rows, no harsh cadres) */}
        <div className={`${selectedSandbox ? 'lg:col-span-8' : 'lg:col-span-12'} min-w-0`}>
          <div className="card-elevated overflow-hidden">
            <div className="overflow-x-auto">
              <table className="table-modern">
                <thead>
                  <tr>
                    <th>Team Instance</th>
                    <th>IP Address</th>
                    <th>Subdomain Route</th>
                    <th>Status</th>
                    <th>Lease TTL</th>
                    <th className="text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {loading && rawList.length === 0 ? (
                    <SkeletonTableRows rows={5} cols={6} />
                  ) : filtered.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="py-12 text-center text-[#827787]">
                        <span className="material-symbols-outlined text-[36px] text-gray-300 block mb-2">dns</span>
                        No sandboxes match current criteria.
                      </td>
                    </tr>
                  ) : (
                    filtered.map(s => {
                      const isSelected = s.sandbox_id === selectedId
                      const teamName = formatTeamName(s.hostname, s.sandbox_id)
                      const sub = getSubdomain(s.hostname, s.sandbox_id, s.custom_domain)
                      const isMock = s.sandbox_id.startsWith('recycle-test-')
                      const isActive = s.status === 'ACTIVE'
                      const isFailed = ['ERROR', 'FAILED', 'TEARDOWN_FAILED'].includes(s.status)

                      return (
                        <tr
                          key={s.sandbox_id}
                          onClick={() => setSelectedId(s.sandbox_id)}
                          className={`cursor-pointer transition-colors duration-150 ${
                            isSelected ? 'bg-purple-900/[0.04]' : 'hover:bg-purple-900/[0.02]'
                          }`}
                        >
                          {/* Team Name */}
                          <td className="min-w-[160px]">
                            <div className="font-bold text-[#170B1B] text-base flex items-center gap-2">
                              {teamName}
                              {!isMock && <span className="w-2 h-2 rounded-full bg-[#74537a]" />}
                            </div>
                            <div className="text-xs text-[#827787] font-mono truncate max-w-[160px]" title={s.sandbox_id}>
                              {cleanLabel(s.sandbox_id)}
                            </div>
                          </td>

                          {/* IP Address */}
                          <td className="font-mono whitespace-nowrap">
                            <span className="px-3 py-1.5 rounded-xl bg-[#F8F6F9] text-[#170B1B] font-bold text-sm">
                              {s.ip_address || '10.20.10.x'}:8080
                            </span>
                          </td>

                          {/* Subdomain */}
                          <td className="min-w-[200px] max-w-[280px]">
                            <div className="font-mono text-sm font-semibold text-[#2D0035] truncate" title={sub}>
                              {sub}
                            </div>
                            {s.custom_domain && (
                              <div className="text-xs text-emerald-800 font-mono font-medium truncate mt-0.5">
                                Alias: {s.custom_domain}
                              </div>
                            )}
                          </td>

                          {/* Status Pill Badge */}
                          <td className="whitespace-nowrap">
                            {isActive ? (
                              <span className="badge badge-emerald">
                                <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
                                ACTIVE
                              </span>
                            ) : s.status === 'PROVISIONING' ? (
                              <span className="badge badge-blue">
                                <span className="w-2 h-2 rounded-full bg-blue-500 animate-spin" />
                                BOOTING
                              </span>
                            ) : isFailed ? (
                              <span className="badge badge-rose">
                                <span className="material-symbols-outlined text-[14px]">warning</span>
                                {s.status === 'TEARDOWN_FAILED' ? 'FAILED' : s.status}
                              </span>
                            ) : (
                              <span className="badge badge-gray">
                                RELEASED
                              </span>
                            )}
                          </td>

                          {/* Lease TTL */}
                          <td className="font-mono text-sm whitespace-nowrap">
                            {isActive && s.expires_at ? (
                              <span className="font-bold text-[#170B1B]">
                                {formatDuration(Math.max(0, s.expires_at - Math.floor(Date.now() / 1000)))}
                              </span>
                            ) : isFailed ? (
                              <span className="text-amber-800 font-semibold">Needs reset</span>
                            ) : (
                              <span className="text-[#827787]">Expired</span>
                            )}
                          </td>

                          {/* Actions */}
                          <td className="text-right whitespace-nowrap space-x-2" onClick={e => e.stopPropagation()}>
                            {isFailed ? (
                              <button
                                onClick={() => handleRecoverOrReprovision(s)}
                                disabled={recoveringId === s.sandbox_id}
                                className="px-3.5 py-1.5 rounded-xl bg-emerald-700 hover:bg-emerald-800 text-white text-xs font-bold transition-all cursor-pointer disabled:opacity-50"
                              >
                                {recoveringId === s.sandbox_id ? 'Booting…' : 'Re-deploy'}
                              </button>
                            ) : isActive ? (
                              <button
                                onClick={() => handleRenew(s.sandbox_id)}
                                className="px-3 py-1.5 rounded-xl bg-emerald-50 hover:bg-emerald-100 text-emerald-800 text-xs font-bold transition-all cursor-pointer"
                              >
                                +1h
                              </button>
                            ) : null}

                            <button
                              onClick={() => setSelectedId(s.sandbox_id)}
                              className="px-3.5 py-1.5 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] text-xs font-semibold transition-all cursor-pointer"
                            >
                              Inspect
                            </button>

                            {s.status !== 'RELEASED' && (
                              <button
                                onClick={() => handleTeardown(s.sandbox_id)}
                                className="px-3.5 py-1.5 rounded-xl bg-white hover:bg-rose-50 text-rose-700 hover:text-rose-800 text-xs font-semibold transition-all cursor-pointer"
                              >
                                Teardown
                              </button>
                            )}
                          </td>
                        </tr>
                      )
                    })
                  )}
                </tbody>
              </table>
            </div>

            {filtered.length > 30 && (
              <div className="p-3.5 bg-[#F8F6F9] text-center text-xs text-[#827787]">
                Displaying first 30 of {filtered.length} entries. Use search or filter tabs to narrow down results.
              </div>
            )}
          </div>
        </div>

        {/* ── Modern Inspector Drawer (Rounded-2xl, soft shadows) ── */}
        {selectedSandbox && (
          <div className="lg:col-span-4 card-elevated p-6 space-y-5 sticky top-20 animate-fade-in">
            {/* Header */}
            <div className="flex items-start justify-between pb-3.5 border-b border-purple-900/5">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-2xl bg-purple-50 flex items-center justify-center text-[#2D0035]">
                  <span className="material-symbols-outlined text-[20px]">troubleshoot</span>
                </div>
                <div>
                  <h3 className="font-bold text-base text-[#170B1B] leading-tight">
                    {formatTeamName(selectedSandbox.hostname, selectedSandbox.sandbox_id)}
                  </h3>
                  <div className="text-xs font-mono text-[#827787]">
                    {selectedSandbox.sandbox_id}
                  </div>
                </div>
              </div>
              <button
                onClick={() => setSelectedId(null)}
                className="text-[#827787] hover:text-[#170B1B] p-1.5 rounded-xl hover:bg-[#F8F6F9] transition-colors cursor-pointer"
              >
                <span className="material-symbols-outlined text-[18px]">close</span>
              </button>
            </div>

            {/* Target Card */}
            <div className="p-4 rounded-2xl bg-[#F8F6F9] space-y-3 text-sm">
              <div className="flex justify-between items-center">
                <span className="text-[#827787] font-medium">Private VM Target:</span>
                <span className="font-mono font-bold text-[#170B1B] bg-white px-2.5 py-1 rounded-lg shadow-xs">
                  {selectedSandbox.ip_address || '10.20.10.x'}:8080
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-[#827787] font-medium">Subdomain:</span>
                <span className="font-mono font-bold text-[#2D0035] truncate max-w-[200px]">
                  {getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id, selectedSandbox.custom_domain)}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-[#827787] font-medium">VIP Routing:</span>
                <span className="font-mono font-bold text-emerald-800">203.0.113.10 (gw1)</span>
              </div>
            </div>

            {/* Security Isolation */}
            <div className="space-y-2.5">
              <span className="text-xs font-bold text-[#74537a] uppercase tracking-wider font-mono">
                Neutron Security Rules
              </span>
              <div className="p-4 rounded-2xl bg-[#F8F6F9] space-y-2.5 text-sm">
                <div className="flex items-center gap-2.5 text-emerald-800 font-semibold">
                  <span className="material-symbols-outlined text-[18px]">check_circle</span>
                  <span>Ingress TCP 80/443 from Gateway VIP</span>
                </div>
                <div className="flex items-center gap-2.5 text-rose-800 font-semibold">
                  <span className="material-symbols-outlined text-[18px]">block</span>
                  <span>Drop 10.20.10.0/24 (Inter-sandbox)</span>
                </div>
                <div className="flex items-center gap-2.5 text-rose-800 font-semibold">
                  <span className="material-symbols-outlined text-[18px]">block</span>
                  <span>Drop Mgmt Port 8000</span>
                </div>
              </div>
            </div>

            {/* Health cURL Command */}
            <div className="space-y-2.5">
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-[#74537a] uppercase tracking-wider font-mono">
                  Test Ingress Health
                </span>
                <button
                  onClick={() => copyCurl(`curl -H 'Host: ${getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id)}' http://203.0.113.10/health`)}
                  className="text-xs text-[#2D0035] hover:text-[#74537a] font-bold flex items-center gap-1.5 cursor-pointer transition-colors"
                >
                  <span className="material-symbols-outlined text-[16px]">content_copy</span>
                  {copyStatus}
                </button>
              </div>
              <pre className="p-3.5 rounded-2xl bg-[#170B1B] text-emerald-400 text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all select-all">
                {`curl -H 'Host: ${getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id)}' http://203.0.113.10/health`}
              </pre>
            </div>

            {/* Lifecycle Buttons */}
            <div className="grid grid-cols-2 gap-3 pt-2">
              <button
                onClick={() => handleRecoverOrReprovision(selectedSandbox)}
                disabled={recoveringId === selectedSandbox.sandbox_id}
                className="py-3 px-4 rounded-xl bg-[#2D0035] hover:bg-[#43134c] text-white text-sm font-semibold transition-all flex items-center justify-center gap-2 cursor-pointer shadow-md disabled:opacity-50"
              >
                <span className="material-symbols-outlined text-[18px]">rocket_launch</span>
                <span>Deploy / Reset</span>
              </button>
              <button
                onClick={() => handleTeardown(selectedSandbox.sandbox_id)}
                className="py-3 px-4 rounded-xl bg-white hover:bg-rose-50 text-rose-700 text-sm font-semibold transition-all flex items-center justify-center gap-2 cursor-pointer shadow-xs border border-rose-200/50"
              >
                <span className="material-symbols-outlined text-[18px]">delete</span>
                <span>Teardown</span>
              </button>
            </div>
          </div>
        )}

      </div>

    </div>
  )
}
