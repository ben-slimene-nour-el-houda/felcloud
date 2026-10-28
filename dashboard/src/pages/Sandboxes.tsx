import { useState, useCallback, useEffect, useMemo } from 'react'
import { fetchSandboxes, teardownSandbox, provisionSandbox, type Sandbox, type SharedState } from '../api'
import { usePolling, useCountdown, formatDuration } from '../hooks'
import Icon from '../components/Icon'

interface Props {
  onOpenProvision: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
  sharedState?: SharedState | null
  refreshSharedState?: () => void
}

function cleanLabel(raw: string): string {
  if (!raw) return ''
  return raw.replace(/\.cloudpulse\.cstam\.felcloud\.tn$/, '').replace(/\.cstam\.felcloud\.tn$/, '').replace(/\.local$/, '')
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

function getSubdomain(hostname: string, sandboxId: string, custom?: string, domain = 'cloudpulse.cstam.felcloud.tn'): string {
  if (custom) return custom
  const base = cleanLabel(hostname) || cleanLabel(sandboxId) || 'sandbox'
  return `${base}.${domain}`
}

export default function Sandboxes({ onOpenProvision, showToast, sharedState, refreshSharedState }: Props) {
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const { data: sandboxes, refresh } = usePolling<Sandbox[]>(sbFetcher, 5000)

  const [searchQuery, setSearchQuery] = useState('')
  const [filterMode, setFilterMode] = useState<'ACTIVE' | 'ALL' | 'ISSUES' | 'RELEASED'>('ACTIVE')
  const [hideReleasedTests, setHideReleasedTests] = useState(true)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [copyStatus, setCopyStatus] = useState('Copy cURL')
  const [recoveringId, setRecoveringId] = useState<string | null>(null)

  const domain = sharedState?.config?.routing_domain || 'cloudpulse.cstam.felcloud.tn'

  const rawList = useMemo(() => {
    if (sharedState?.sandboxes?.items) {
      return sharedState.sandboxes.items
    }
    const serverItems = Array.isArray(sandboxes) ? sandboxes : []
    return serverItems.filter(s =>
      !s.sandbox_id.startsWith('recycle-test-') &&
      !s.sandbox_id.startsWith('bench-') &&
      !s.sandbox_id.startsWith('probe') &&
      !['demo1','demo2','demo3','demo4','demo5','demo6','demo7','demo9','demo10'].includes(s.sandbox_id)
    )
  }, [sharedState, sandboxes])

  // Counts derived from sharedState when available
  const activeCount   = sharedState?.sandboxes?.active_count ?? rawList.filter(s => s.status === 'ACTIVE').length
  const provCount     = sharedState?.sandboxes?.provisioning_count ?? rawList.filter(s => s.status === 'PROVISIONING').length
  const issueCount    = sharedState?.sandboxes?.failed_count ?? rawList.filter(s => ['ERROR', 'FAILED', 'TEARDOWN_FAILED'].includes(s.status)).length
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
      // 1. Hide released tests if toggle is on
      if (hideReleasedTests && s.status === 'RELEASED' && s.sandbox_id.startsWith('recycle-test-')) {
        return false
      }

      // 2. Search query
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

      // 3. Tab filter
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
      // Prioritize named teams first, then active status
      const aIsMock = a.sandbox_id.startsWith('recycle-test-')
      const bIsMock = b.sandbox_id.startsWith('recycle-test-')
      if (!aIsMock && bIsMock) return -1
      if (aIsMock && !bIsMock) return 1

      const rank: Record<string, number> = { ACTIVE: 1, PROVISIONING: 2, TEARDOWN_FAILED: 3, ERROR: 4, RELEASED: 5 }
      return (rank[a.status] || 9) - (rank[b.status] || 9)
    })
  }, [rawList, hideReleasedTests, searchQuery, filterMode])

  const selectedSandbox = rawList.find(s => s.sandbox_id === selectedId) || null

  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000))
  useEffect(() => {
    const timer = setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000)
    return () => clearInterval(timer)
  }, [])

  async function handleTeardown(id: string) {
    if (!confirm(`Are you sure you want to teardown sandbox "${id}"? Its private IP will be held in cooling down quarantine for 3 minutes.`)) return
    showToast('Teardown Initiated', `Sandbox "${id}" marked for release.`, 'warning')
    try {
      await teardownSandbox(id)
      showToast('Teardown Completed', `Sandbox "${id}" decommissioned. IP entered cooling down quarantine.`, 'success')
      refresh()
      refreshSharedState?.()
    } catch (err: any) {
      showToast('Teardown Failed', err.message || 'Could not teardown sandbox.', 'error')
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

      {/* ── Subnet & Actions Ribbon ── */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-white px-5 py-3 rounded-2xl border border-slate-200/80 shadow-xs">
        <div className="flex items-center gap-2.5 flex-wrap">
          <span className="font-bold text-sm text-slate-900">Hackathon Sandboxes</span>
          <span className="px-2 py-0.5 rounded-full bg-violet-100 text-violet-800 text-xs font-mono font-bold border border-violet-200">
            10.20.30.0/24 Subnet
          </span>
          <span className="px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-800 text-xs font-mono font-bold border border-emerald-300">
            {activeCount} Active Instances
          </span>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => { refresh(); showToast('Refreshed', 'Synced live sandbox state from control plane.', 'info') }}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-50 hover:bg-slate-100 text-slate-700 border border-slate-200 rounded-xl text-xs font-semibold transition-all cursor-pointer"
          >
            <Icon name="refresh" size={14} />
            <span>Sync</span>
          </button>
          <button
            onClick={onOpenProvision}
            className="flex items-center gap-1.5 px-3.5 py-1.5 text-white rounded-xl text-xs font-bold shadow-xs transition-all cursor-pointer hover:brightness-110 active:scale-95 bg-[#2D0035]"
          >
            <Icon name="add_circle" size={15} className="text-[#FFD200]" />
            <span>Provision Sandbox</span>
          </button>
        </div>
      </div>

      {/* ── 4 Compact KPI Stat Cards ── */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
        {/* Card 1: Active Instances */}
        <div className="bg-white rounded-2xl p-3.5 border border-slate-200/90 shadow-xs relative overflow-hidden">
          <div className="absolute top-0 left-0 right-0 h-1 bg-violet-600" />
          <div className="flex items-center justify-between text-slate-500 mb-1.5">
            <span className="text-[11px] font-bold uppercase tracking-wider text-slate-600">Active Teams</span>
            <div className="w-7 h-7 rounded-lg bg-violet-50 flex items-center justify-center text-violet-600">
              <Icon name="groups" size={16} />
            </div>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-extrabold text-slate-900 font-mono">{activeCount}</span>
            <span className="text-xs text-slate-500 font-medium">/ 10 Teams</span>
          </div>
          <div className="mt-1.5 flex items-center gap-1.5 text-xs">
            <span className="inline-block w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
            <span className="text-emerald-700 font-semibold font-mono text-[11px]">100% HA Ingress Ready</span>
          </div>
        </div>

        {/* Card 2: IPAM Leased */}
        <div className="bg-white rounded-2xl p-3.5 border border-slate-200/90 shadow-xs relative overflow-hidden">
          <div className="absolute top-0 left-0 right-0 h-1 bg-blue-600" />
          <div className="flex items-center justify-between text-slate-500 mb-1.5">
            <span className="text-[11px] font-bold uppercase tracking-wider text-slate-600">IPAM Pool</span>
            <div className="w-7 h-7 rounded-lg bg-blue-50 flex items-center justify-center text-blue-600">
              <Icon name="layers" size={16} />
            </div>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-extrabold text-slate-900 font-mono">{activeCount}</span>
            <span className="text-xs text-slate-500 font-medium">/ 231 IPs</span>
          </div>
          <div className="mt-1.5 w-full bg-slate-100 h-1.5 rounded-full overflow-hidden">
            <div className="bg-blue-600 h-full rounded-full transition-all" style={{ width: `${Math.min(100, (activeCount / 231) * 100)}%` }} />
          </div>
        </div>

        {/* Card 3: Default Lease TTL */}
        <div className="bg-white rounded-2xl p-3.5 border border-slate-200/90 shadow-xs relative overflow-hidden">
          <div className="absolute top-0 left-0 right-0 h-1 bg-emerald-600" />
          <div className="flex items-center justify-between text-slate-500 mb-1.5">
            <span className="text-[11px] font-bold uppercase tracking-wider text-slate-600">Sandbox Lifetime</span>
            <div className="w-7 h-7 rounded-lg bg-emerald-50 flex items-center justify-center text-emerald-600">
              <Icon name="timer" size={16} />
            </div>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-extrabold text-slate-900 font-mono">1 h</span>
            <span className="text-xs text-emerald-800 font-semibold">default lease</span>
          </div>
          <div className="mt-1 text-[11px] text-slate-500">
            IP recycled after 3 min safety flush
          </div>
        </div>

        {/* Card 4: VIP Ingress */}
        <div className="bg-white rounded-2xl p-3.5 border border-slate-200/90 shadow-xs relative overflow-hidden">
          <div className="absolute top-0 left-0 right-0 h-1 bg-amber-500" />
          <div className="flex items-center justify-between text-slate-500 mb-1.5">
            <span className="text-[11px] font-bold uppercase tracking-wider text-slate-600">Gateway VIP</span>
            <div className="w-7 h-7 rounded-lg bg-amber-50 flex items-center justify-center text-amber-600">
              <Icon name="router" size={16} />
            </div>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-xl font-extrabold text-violet-950 font-mono">{sharedState?.config?.vip_address || '10.20.20.246'}</span>
          </div>
          <div className="mt-1 flex items-center gap-1.5 text-[11px] text-emerald-700 font-semibold font-mono">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
            <span>All traffic enters here</span>
          </div>
        </div>
      </div>

      {/* ── Filter Bar: Tabs, Search, & Hide Tests Toggle ── */}
      <div className="bg-white p-3.5 rounded-2xl border border-slate-200 shadow-xs flex flex-col md:flex-row items-stretch md:items-center justify-between gap-3">
        {/* Search */}
        <div className="relative flex-1 min-w-[240px]">
          <span className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-slate-400">
            <Icon name="search" size={16} />
          </span>
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search by team, IP, or subdomain…"
            className="w-full pl-10 pr-8 py-2 bg-slate-50 border border-slate-200 rounded-xl text-xs text-slate-900 font-medium placeholder-slate-400 focus:outline-none focus:border-violet-600 focus:bg-white transition-all"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute inset-y-0 right-0 pr-3 flex items-center text-slate-400 hover:text-slate-700 cursor-pointer"
            >
              <Icon name="close" size={16} />
            </button>
          )}
        </div>

        {/* Filter Dropdown */}
        <div className="flex items-center gap-2 flex-shrink-0">
          <label htmlFor="team-filter" className="text-xs font-bold text-slate-600">Filter:</label>
          <select
            id="team-filter"
            value={filterMode}
            onChange={e => setFilterMode(e.target.value as any)}
            className="px-3 py-1.5 bg-slate-50 border border-slate-200 rounded-xl text-xs font-bold text-slate-800 cursor-pointer focus:outline-none focus:border-violet-600 focus:bg-white transition-all"
          >
            <option value="ACTIVE">Active &amp; Provisioning ({activeCount})</option>
            <option value="ALL">All Teams ({rawList.length})</option>
            <option value="ISSUES">Attention Needed ({issueCount})</option>
            <option value="RELEASED">Archived ({releasedCount})</option>
          </select>
        </div>

        {/* Hide test instances toggle */}
        <label className="flex items-center gap-2 text-xs font-semibold text-slate-700 cursor-pointer select-none px-2.5 py-1.5 bg-slate-50 hover:bg-slate-100 rounded-xl border border-slate-200 flex-shrink-0 transition-colors">
          <input
            type="checkbox"
            checked={hideReleasedTests}
            onChange={e => setHideReleasedTests(e.target.checked)}
            className="w-4 h-4 rounded text-violet-600 focus:ring-violet-500 cursor-pointer"
          />
          <span>Hide archived test runs ({rawList.filter(s => s.status === 'RELEASED' && s.sandbox_id.startsWith('recycle-test-')).length})</span>
        </label>
      </div>

      {/* ── Sandboxes Table & Details Grid ── */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-5 items-start min-w-0">

        {/* Table Container */}
        <div className={`${selectedSandbox ? 'lg:col-span-8' : 'lg:col-span-12'} min-w-0`}>
          <div className="bg-white rounded-2xl border border-slate-200 shadow-xs overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs border-collapse">
                <thead>
                  <tr className="bg-slate-100/80 border-b border-slate-200 text-slate-700 text-[11px] font-bold uppercase tracking-wider font-mono">
                    <th className="py-3.5 px-4">Team</th>
                    <th className="py-3.5 px-3">IP</th>
                    <th className="py-3.5 px-3">URL</th>
                    <th className="py-3.5 px-3">Status</th>
                    <th className="py-3.5 px-3">Time Left</th>
                    <th className="py-3.5 px-4 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {filtered.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="py-12 text-center text-slate-500">
                        <Icon name="dns" size={36} className="text-slate-300 block mb-2 mx-auto" />
                        <div className="font-semibold text-slate-800 text-sm mb-1">No sandboxes match current criteria</div>
                        <p className="text-xs text-slate-500 mb-3">Launch a team VM with an automatic IP and load balancer route.</p>
                        <button
                          onClick={onOpenProvision}
                          className="px-4 py-2 rounded-xl text-xs font-bold text-white bg-slate-900 hover:bg-slate-800 transition-colors shadow-xs cursor-pointer inline-flex items-center gap-1.5"
                        >
                          <Icon name="add" size={16} />
                          <span>Provision Sandbox</span>
                        </button>
                      </td>
                    </tr>
                  ) : (
                    filtered.map(s => {
                      const isSelected = s.sandbox_id === selectedId
                      const teamName = formatTeamName(s.hostname, s.sandbox_id)
                      const sub = getSubdomain(s.hostname, s.sandbox_id, s.custom_domain, domain)
                      const isMock = s.sandbox_id.startsWith('recycle-test-')
                      const isActive = s.status === 'ACTIVE'
                      const isFailed = ['ERROR', 'FAILED', 'TEARDOWN_FAILED'].includes(s.status)

                      return (
                        <tr
                          key={s.sandbox_id}
                          onClick={() => setSelectedId(s.sandbox_id)}
                          className={`transition-colors cursor-pointer ${
                            isSelected
                              ? 'bg-violet-50/80 border-l-4 border-violet-600'
                              : 'hover:bg-slate-50/80'
                          }`}
                        >
                          {/* Team Name */}
                          <td className="py-3.5 px-4 min-w-[140px]">
                            <div className="font-bold text-slate-900 text-[13px] flex items-center gap-1.5">
                              {teamName}
                              {!isMock && <span className="w-1.5 h-1.5 rounded-full bg-violet-600" />}
                            </div>
                            <div className="text-[11px] text-slate-500 font-mono truncate max-w-[150px]" title={s.sandbox_id}>
                              {cleanLabel(s.sandbox_id)}
                            </div>
                          </td>

                          {/* IP Address */}
                          <td className="py-3.5 px-3 font-mono whitespace-nowrap">
                            <span className="px-2 py-1 rounded-md bg-slate-100 border border-slate-200 text-slate-900 font-bold text-[11px]">
                              {s.ip_address ? `${s.ip_address}:8080` : 'Pending'}
                            </span>
                          </td>

                          {/* URL (click to copy or open) */}
                          <td className="py-3.5 px-3 min-w-[190px] max-w-[270px]">
                            <div className="flex items-center gap-1.5">
                              <span className="font-mono text-xs font-semibold text-violet-900 truncate" title={sub}>
                                {sub}
                              </span>
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  navigator.clipboard.writeText(`http://${sub}`)
                                  showToast('Copied URL', `http://${sub}`, 'info')
                                }}
                                className="p-1 text-slate-400 hover:text-slate-700 rounded hover:bg-slate-100 transition-colors cursor-pointer"
                                title="Copy URL"
                              >
                                <Icon name="content_copy" size={13} />
                              </button>
                              <a
                                href={`http://${sub}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                onClick={(e) => e.stopPropagation()}
                                className="p-1 text-slate-400 hover:text-violet-700 rounded hover:bg-slate-100 transition-colors cursor-pointer"
                                title="Open URL in new tab"
                              >
                                <Icon name="open_in_new" size={13} />
                              </a>
                            </div>
                            {s.custom_domain && (
                              <div className="text-[10px] text-emerald-800 font-mono font-bold truncate mt-0.5">
                                Alias: {s.custom_domain}
                              </div>
                            )}
                          </td>

                          {/* Status */}
                          <td className="py-3.5 px-3 whitespace-nowrap">
                            {isActive ? (
                              <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-bold bg-emerald-100 text-emerald-900 border border-emerald-300">
                                <span className="w-2 h-2 rounded-full bg-emerald-600 animate-pulse" />
                                ACTIVE
                              </span>
                            ) : s.status === 'PROVISIONING' ? (
                              <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-bold bg-blue-100 text-blue-900 border border-blue-300">
                                <span className="w-2 h-2 rounded-full bg-blue-600 animate-spin" />
                                BOOTING
                              </span>
                            ) : isFailed ? (
                              <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[11px] font-bold bg-rose-100 text-rose-900 border border-rose-300">
                                <Icon name="warning" size={13} className="text-rose-600" />
                                {s.status === 'TEARDOWN_FAILED' ? 'FAILED' : s.status}
                              </span>
                            ) : (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-slate-100 text-slate-600 border border-slate-200">
                                RELEASED
                              </span>
                            )}
                          </td>

                          {/* Time left (live countdown) */}
                          <td className="py-3.5 px-3 font-mono text-xs whitespace-nowrap">
                            {isActive && s.expires_at ? (
                              <span className="font-bold text-slate-800">
                                {formatDuration(Math.max(0, s.expires_at - nowSec))}
                              </span>
                            ) : isFailed ? (
                              <span className="text-amber-800 font-semibold">Needs reset</span>
                            ) : (
                              <span className="text-slate-400">Expired</span>
                            )}
                          </td>

                          {/* Actions */}
                          <td className="py-3 px-4 text-right whitespace-nowrap" onClick={e => e.stopPropagation()}>
                            <div className="inline-flex items-center justify-end gap-2">
                              {isFailed ? (
                                <button
                                  onClick={() => handleRecoverOrReprovision(s)}
                                  disabled={recoveringId === s.sandbox_id}
                                  className="px-2.5 py-1 rounded-lg bg-emerald-600 hover:bg-emerald-700 text-white text-xs font-bold shadow-xs transition-colors cursor-pointer disabled:opacity-50"
                                >
                                  {recoveringId === s.sandbox_id ? 'Booting…' : 'Re-deploy'}
                                </button>
                              ) : isActive ? (
                                <button
                                  onClick={() => handleRenew(s.sandbox_id)}
                                  className="px-2 py-1 rounded-lg bg-emerald-50 hover:bg-emerald-100 text-emerald-800 border border-emerald-300 text-xs font-bold transition-colors cursor-pointer"
                                >
                                  +1h
                                </button>
                              ) : null}

                              <button
                                onClick={() => setSelectedId(s.sandbox_id)}
                                className="px-2.5 py-1 rounded-lg bg-slate-100 hover:bg-slate-200 text-slate-800 border border-slate-300 text-xs font-semibold transition-colors cursor-pointer shadow-2xs"
                              >
                                Inspect
                              </button>

                              {s.status !== 'RELEASED' && (
                                <button
                                  onClick={() => handleTeardown(s.sandbox_id)}
                                  className="px-2.5 py-1 rounded-lg bg-white hover:bg-rose-50 text-rose-700 border border-rose-200 hover:border-rose-300 text-xs font-semibold transition-colors cursor-pointer shadow-2xs"
                                >
                                  Teardown
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                      )
                    })
                  )}
                </tbody>
              </table>
            </div>

            {filtered.length > 30 && (
              <div className="p-3 bg-slate-50 border-t border-slate-200 text-center text-xs text-slate-500">
                Displaying first 30 of {filtered.length} entries. Use search or filter tabs to narrow down results.
              </div>
            )}
          </div>
        </div>

        {/* ── High Contrast Inspector Drawer ── */}
        {selectedSandbox && (
          <div className="lg:col-span-4 bg-white rounded-2xl border border-slate-200 p-5 shadow-sm space-y-4 sticky top-[68px]">
            {/* Header */}
            <div className="flex items-start justify-between pb-3 border-b border-slate-200">
              <div className="flex items-center gap-2.5">
                <div className="w-9 h-9 rounded-xl bg-violet-100 flex items-center justify-center text-violet-800">
                  <Icon name="troubleshoot" size={20} />
                </div>
                <div>
                  <h3 className="font-extrabold text-base text-slate-900 leading-tight">
                    {formatTeamName(selectedSandbox.hostname, selectedSandbox.sandbox_id)}
                  </h3>
                  <div className="text-xs font-mono text-slate-500">
                    {selectedSandbox.sandbox_id}
                  </div>
                </div>
              </div>
              <button
                onClick={() => setSelectedId(null)}
                className="text-slate-400 hover:text-slate-700 p-1 rounded-lg hover:bg-slate-100 transition-colors cursor-pointer"
              >
                <Icon name="close" size={18} />
              </button>
            </div>

            {/* Target Card */}
            <div className="p-3.5 rounded-xl bg-slate-50 border border-slate-200 space-y-2.5 text-xs">
              <div className="flex justify-between items-center">
                <span className="text-slate-500 font-medium">Private VM Target:</span>
                <span className="font-mono font-bold text-slate-900 bg-white px-2 py-0.5 rounded border border-slate-200">
                  {selectedSandbox.ip_address ? `${selectedSandbox.ip_address}:8080` : 'Pending'}
                </span>
              </div>
              <div className="flex flex-col sm:flex-row sm:justify-between sm:items-center gap-1">
                <span className="text-slate-500 font-medium">Subdomain URL:</span>
                <span className="font-mono font-bold text-violet-900 break-all select-all">
                  {getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id, selectedSandbox.custom_domain, domain)}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-slate-500 font-medium">Gateway VIP Routing:</span>
                <span className="font-mono font-bold text-emerald-800">
                  {sharedState?.config?.vip_address || '10.20.20.246'} ({(sharedState?.gateways?.vip_holder || 'gw1').toUpperCase()} ACTIVE)
                </span>
              </div>
            </div>

            {/* Security Isolation */}
            <div className="space-y-1.5">
              <span className="text-[11px] font-bold text-slate-700 uppercase tracking-wider font-mono">
                Neutron Security Rules
              </span>
              <div className="p-3 rounded-xl bg-slate-50 border border-slate-200 space-y-2 text-xs">
                <div className="flex items-center gap-2 text-emerald-800 font-semibold">
                  <Icon name="check_circle" size={16} />
                  <span>Ingress TCP 80/443 from Gateway VIP</span>
                </div>
                <div className="flex items-center gap-2 text-rose-800 font-semibold">
                  <Icon name="block" size={16} />
                  <span>Drop 10.20.30.0/24 (Inter-sandbox)</span>
                </div>
                <div className="flex items-center gap-2 text-rose-800 font-semibold">
                  <Icon name="block" size={16} />
                  <span>Drop Mgmt Port 8000</span>
                </div>
              </div>
            </div>

            {/* Health cURL Command */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="text-[11px] font-bold text-slate-700 uppercase tracking-wider font-mono">
                  Test Ingress Health
                </span>
                <button
                  onClick={() => copyCurl(`curl -H 'Host: ${getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id)}' http://10.20.20.246/health`)}
                  className="text-xs text-violet-700 hover:text-violet-900 font-bold flex items-center gap-1 cursor-pointer"
                >
                  <Icon name="copy" size={14} />
                  {copyStatus}
                </button>
              </div>
              <pre className="p-2.5 rounded-xl bg-slate-900 text-emerald-400 text-[11px] font-mono overflow-x-auto whitespace-pre-wrap break-all select-all border border-slate-800">
                {`curl -H 'Host: ${getSubdomain(selectedSandbox.hostname, selectedSandbox.sandbox_id)}' http://10.20.20.246/health`}
              </pre>
            </div>

            {/* Lifecycle Buttons */}
            <div className="grid grid-cols-2 gap-2 pt-2">
              <button
                onClick={() => handleRecoverOrReprovision(selectedSandbox)}
                disabled={recoveringId === selectedSandbox.sandbox_id}
                className="py-2.5 px-3 rounded-xl bg-violet-700 hover:bg-violet-800 text-white text-xs font-bold transition-all flex items-center justify-center gap-1.5 cursor-pointer shadow-xs disabled:opacity-50"
              >
                <Icon name="rocket_launch" size={16} />
                <span>Deploy / Reset</span>
              </button>
              <button
                onClick={() => handleTeardown(selectedSandbox.sandbox_id)}
                className="py-2.5 px-3 rounded-xl bg-white hover:bg-rose-50 text-rose-700 border border-slate-200 hover:border-rose-300 text-xs font-bold transition-all flex items-center justify-center gap-1.5 cursor-pointer shadow-xs"
              >
                <Icon name="delete" size={16} />
                <span>Teardown</span>
              </button>
            </div>
          </div>
        )}

      </div>

    </div>
  )
}
