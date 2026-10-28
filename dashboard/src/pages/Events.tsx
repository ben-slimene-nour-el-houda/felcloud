import { useState, useCallback } from 'react'
import { fetchEvents, type EventEntry, type SharedState } from '../api'
import { usePolling } from '../hooks'
import Icon from '../components/Icon'

interface Props {
  sharedState?: SharedState | null
  refreshSharedState?: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

interface AuditRow {
  id: string
  time: string
  timeAgo: string
  actor: string
  action: string
  target: string
  severity: 'CRITICAL' | 'WARNING' | 'INFO'
  category: string
  details: string
  accordion: {
    panel1Title: string
    panel1Lines: string[]
    panel2Title: string
    panel2Lines: string[]
    panel3Title: string
    panel3Desc: string
    actionBtnText?: string
    actionBtnType?: 'error' | 'default'
  }
}

export default function Events({ sharedState, refreshSharedState, showToast }: Props) {
  const evFetcher = useCallback(() => fetchEvents(100), [])
  const { data: evData } = usePolling<{ events: EventEntry[] }>(evFetcher, 6000)

  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})
  const [showSecurityDrawer, setShowSecurityDrawer] = useState(false)
  const [range, setRange] = useState<'1h' | '24h' | '7d'>('24h')
  const [severityFilter, setSeverityFilter] = useState('ALL')
  const [categoryFilter, setCategoryFilter] = useState('ALL')
  const [actorFilter, setActorFilter] = useState('ALL')
  const [searchQuery, setSearchQuery] = useState('')

  const realRows: AuditRow[] = (evData?.events || []).map((e, idx) => {
    const isError = e.event_type.includes('ERROR') || e.event_type.includes('FAIL')
    const isWarning = e.event_type.includes('QUARANTINE') || e.event_type.includes('BLOCK')
    const sev: 'CRITICAL' | 'WARNING' | 'INFO' = isError ? 'CRITICAL' : (isWarning ? 'WARNING' : 'INFO')

    let cat = 'PLATFORM'
    const evType = (e.event_type || e.type || '').toUpperCase()
    if (evType.includes('SANDBOX') || e.scope.includes('sandbox')) cat = 'SANDBOX'
    else if (evType.includes('IP') || evType.includes('LEASE')) cat = 'IPAM'
    else if (evType.includes('GATEWAY') || evType.includes('VRRP') || evType.includes('FAILOVER')) cat = 'GATEWAY'
    else if (evType.includes('CONFIG') || evType.includes('ROLLBACK')) cat = 'CONFIG'
    else if (evType.includes('SECURITY')) cat = 'SECURITY'

    return {
      id: `evt-${e.id || idx}`,
      time: e.created_at ? new Date(e.created_at).toLocaleTimeString() : 'Recent',
      timeAgo: e.created_at ? new Date(e.created_at).toLocaleString() : 'Just now',
      actor: e.scope || 'system',
      action: e.event_type || e.type || 'EVENT',
      target: e.scope || 'cluster',
      severity: sev,
      category: cat,
      details: e.detail || `${e.event_type || e.type} (${e.duration_ms}ms)`,
      accordion: {
        panel1Title: 'EVENT METRICS',
        panel1Lines: [
          `SCOPE: ${e.scope || 'cluster'}`,
          `TYPE: ${e.event_type || e.type}`,
          `DURATION: ${e.duration_ms} ms`,
        ],
        panel2Title: 'LEDGER RECORD',
        panel2Lines: [
          `RECORD ID: ${e.id}`,
          `TIMESTAMP: ${e.created_at}`,
          `STATUS: Recorded in SQLite events table`,
        ],
        panel3Title: 'EVENT DETAIL',
        panel3Desc: e.detail || 'Event successfully recorded by cluster daemon.',
      },
    }
  })

  const actors = ['ALL', ...Array.from(new Set(realRows.map(r => r.actor)))]

  // Filter rows
  const filteredRows = realRows.filter(r => {
    const q = searchQuery.toLowerCase()
    const matchesSearch = r.action.toLowerCase().includes(q) ||
      r.target.toLowerCase().includes(q) ||
      r.details.toLowerCase().includes(q) ||
      r.actor.toLowerCase().includes(q)

    let matchesSev = true
    if (severityFilter === 'CRITICAL') matchesSev = r.severity === 'CRITICAL'
    else if (severityFilter === 'WARNING') matchesSev = r.severity === 'WARNING'
    else if (severityFilter === 'INFO') matchesSev = r.severity === 'INFO'

    let matchesCat = true
    if (categoryFilter !== 'ALL') matchesCat = r.category === categoryFilter

    let matchesActor = true
    if (actorFilter !== 'ALL') matchesActor = r.actor === actorFilter

    return matchesSearch && matchesSev && matchesCat && matchesActor
  })

  function toggleRow(id: string) {
    setExpandedRows(prev => ({ ...prev, [id]: !prev[id] }))
  }

  function handleExportCsv() {
    const csvContent = "data:text/csv;charset=utf-8,"
      + ["Time,Actor,Action,Target,Severity,Category,Details"].join(",") + "\n"
      + filteredRows.map(r => `"${r.time}","${r.actor}","${r.action}","${r.target}","${r.severity}","${r.category}","${(r.details || '').replace(/"/g, '""')}"`).join("\n")
    const encodedUri = encodeURI(csvContent)
    const link = document.createElement("a")
    link.setAttribute("href", encodedUri)
    link.setAttribute("download", "felcloud-events.csv")
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    showToast('Export Complete', `Exported ${filteredRows.length} events to felcloud-events.csv`, 'info')
  }

  function cycleActor() {
    const idx = actors.indexOf(actorFilter)
    const next = actors[(idx + 1) % actors.length]
    setActorFilter(next)
  }

  return (
    <div className="space-y-6">
      {/* Header section with live status & operational triggers */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 bg-white p-6 rounded-xl border border-[#E8E0EA] shadow-xs">
        <div className="space-y-1.5">
          <div className="flex items-center gap-2.5 flex-wrap">
            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full bg-[#F8F6F9] border border-[#E8E0EA] text-xs font-mono text-secondary">
              <span className="w-1.5 h-1.5 rounded-full bg-secondary"></span>
              CLUSTER AUDIT TRAIL
            </span>
            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full bg-[#F8F6F9] border border-[#E8E0EA] text-xs font-mono text-[#827787]">
              SOURCE: <span className="text-secondary font-semibold">SQLite Ledger</span>
            </span>
          </div>
          <h1 className="text-[22px] font-bold text-[#170B1B] tracking-tight">Cluster Security &amp; Operations Audit Stream</h1>
          <p className="text-xs text-[#827787] max-w-3xl">
            Every action taken on the cluster — sandbox launches, IP assignments, gateway failovers, and syncs — is recorded here in real time with full context.
          </p>
        </div>
        <div className="flex items-center gap-2.5 flex-wrap">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#F8F6F9] border border-[#E8E0EA] text-xs">
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-secondary opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2 w-2 bg-secondary"></span>
            </span>
            <span className="font-semibold text-secondary">LOGGING LIVE</span>
            <span className="text-[#827787] font-mono text-[10px]">{realRows.length} events</span>
          </div>
          <button
            onClick={() => setShowSecurityDrawer(!showSecurityDrawer)}
            className="px-3.5 py-2 rounded-lg bg-[#F8F6F9] hover:bg-[#E8E0EA] border border-[#E8E0EA] text-[#170B1B] text-xs font-semibold flex items-center gap-2 transition-colors shadow-xs"
          >
            <Icon name="policy" size={16} className="text-rose-600" />
            <span>Summary</span>
            <span className="px-1.5 py-0.5 bg-error text-white rounded font-mono text-[10px] font-semibold">{realRows.filter(r => r.severity === 'CRITICAL').length} Critical</span>
          </button>
          <button
            onClick={handleExportCsv}
            className="px-3.5 py-2 rounded-lg bg-[#F8F6F9] hover:bg-[#E8E0EA] border border-[#E8E0EA] text-[#170B1B] text-xs font-semibold flex items-center gap-2 transition-colors shadow-xs"
          >
            <Icon name="download" size={16} />
            <span>Export CSV</span>
          </button>
        </div>
      </div>

      {/* KPI metric cards grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        {/* Card 1: Total Events */}
        <div className="bg-white border border-[#E8E0EA] p-4 rounded-xl shadow-xs flex flex-col justify-between relative overflow-hidden">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] font-medium text-[#827787] uppercase tracking-wider">TOTAL EVENTS</span>
            <Icon name="layers" size={18} className="text-slate-400" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-semibold text-[#170B1B] font-mono">{realRows.length}</span>
            <span className="text-xs text-secondary font-mono">Recorded</span>
          </div>
          <div className="mt-3 flex items-center gap-1.5">
            <span className="text-xs text-[#827787] font-mono">SQLite Events Table</span>
          </div>
          <div className="absolute right-0 top-0 bottom-0 w-1 bg-secondary rounded-r"></div>
        </div>

        {/* Card 2: Security Denials */}
        <div className="bg-white border border-[#E8E0EA] p-4 rounded-xl shadow-xs flex flex-col justify-between relative overflow-hidden">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] font-medium text-rose-600 uppercase tracking-wider">CRITICAL / WARNINGS</span>
            <Icon name="warning" size={18} className="text-rose-600" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-semibold text-error font-mono">{realRows.filter(r => r.severity === 'CRITICAL' || r.severity === 'WARNING').length}</span>
            <span className="text-xs text-error font-mono">alerts</span>
          </div>
          <div className="mt-3 flex items-center justify-between text-xs text-[#827787]">
            <span>Filter by critical</span>
            <span className="font-mono text-error font-medium">{realRows.filter(r => r.severity === 'CRITICAL').length} critical</span>
          </div>
          <div className="absolute right-0 top-0 bottom-0 w-1 bg-error rounded-r"></div>
        </div>

        {/* Card 3: Gateway VRRP Shifts */}
        <div className="bg-white border border-[#E8E0EA] p-4 rounded-xl shadow-xs flex flex-col justify-between relative overflow-hidden">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] font-medium text-[#827787] uppercase tracking-wider">GATEWAY EVENTS</span>
            <Icon name="sync" size={18} className="text-violet-600" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-semibold text-[#170B1B] font-mono">{realRows.filter(r => r.category === 'GATEWAY').length}</span>
            <span className="text-xs text-[#827787] font-mono">recorded</span>
          </div>
          <div className="mt-3 flex items-center justify-between text-xs text-[#827787]">
            <span>Current VIP owner</span>
            <span className="font-mono text-secondary font-medium">{sharedState?.gateways?.vip_holder || 'gw1'} (ACTIVE)</span>
          </div>
          <div className="absolute right-0 top-0 bottom-0 w-1 bg-secondary-container rounded-r"></div>
        </div>

        {/* Card 4: IPAM Lifecycle */}
        <div className="bg-white border border-[#E8E0EA] p-4 rounded-xl shadow-xs flex flex-col justify-between relative overflow-hidden">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] font-medium text-[#827787] uppercase tracking-wider">SANDBOX &amp; IPAM</span>
            <Icon name="dns" size={18} className="text-slate-400" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-semibold text-[#170B1B] font-mono">{realRows.filter(r => r.category === 'SANDBOX' || r.category === 'IPAM').length}</span>
            <span className="text-xs text-[#827787] font-mono">lifecycle events</span>
          </div>
          <div className="mt-3 flex items-center justify-between text-xs text-[#827787]">
            <span>Subnet {sharedState?.ipam.subnet || '10.20.30.0/24'}</span>
            <span className="font-mono text-secondary">{sharedState?.ipam.quarantined ?? 0} in cooldown</span>
          </div>
          <div className="absolute right-0 top-0 bottom-0 w-1 bg-[#E8E0EA] rounded-r"></div>
        </div>
      </div>

      {/* Dense filter bar */}
      <div className="bg-white border border-[#E8E0EA] p-3 rounded-xl shadow-xs flex flex-col lg:flex-row items-stretch lg:items-center justify-between gap-3">
        {/* Left filters */}
        <div className="flex flex-wrap items-center gap-2">
          {/* Range pill */}
          <div className="inline-flex p-1 bg-[#F8F6F9] border border-[#E8E0EA] rounded-lg text-xs font-mono">
            {(['1h', '24h', '7d'] as const).map(r => (
              <button
                key={r}
                onClick={() => setRange(r)}
                className={`px-2.5 py-1 rounded-md transition-colors ${range === r ? 'bg-white text-[#170B1B] shadow-xs font-semibold' : 'text-[#827787] hover:text-[#170B1B]'}`}
              >
                {r}
              </button>
            ))}
          </div>

          {/* Severity selector */}
          <select
            value={severityFilter}
            onChange={e => setSeverityFilter(e.target.value)}
            className="bg-[#F8F6F9] hover:bg-[#E8E0EA] border border-[#E8E0EA] text-[#170B1B] rounded-lg px-3 py-1.5 text-xs font-medium cursor-pointer focus:outline-none"
          >
            <option value="ALL">Severity: All Levels</option>
            <option value="CRITICAL">Critical &amp; Denials</option>
            <option value="WARNING">Warning</option>
            <option value="INFO">Info</option>
          </select>

          {/* Category selector */}
          <select
            value={categoryFilter}
            onChange={e => setCategoryFilter(e.target.value)}
            className="bg-[#F8F6F9] hover:bg-[#E8E0EA] border border-[#E8E0EA] text-[#170B1B] rounded-lg px-3 py-1.5 text-xs font-medium cursor-pointer focus:outline-none"
          >
            <option value="ALL">Category: All Components</option>
            <option value="SECURITY">Security Block</option>
            <option value="IPAM">IPAM Lifecycle</option>
            <option value="VRRP">VRRP / Gateway HA</option>
            <option value="CONFIG">Config Generation</option>
            <option value="SANDBOX">Sandbox Lifecycle</option>
          </select>

          {/* Actor filter button */}
          <button
            onClick={cycleActor}
            className="px-3 py-1.5 rounded-lg bg-[#F8F6F9] hover:bg-[#E8E0EA] border border-[#E8E0EA] text-[#170B1B] text-xs font-medium flex items-center gap-1.5 transition-colors"
          >
            <Icon name="groups" size={16} className="text-slate-400" />
            <span>Actor: <strong className="font-mono text-violet-700">{actorFilter === 'ALL' ? 'Any' : actorFilter}</strong></span>
          </button>
        </div>

        {/* Right search box */}
        <div className="flex items-center gap-2">
          <div className="relative flex-1 lg:w-72">
            <span className="absolute inset-y-0 left-3 flex items-center pointer-events-none text-[#827787]">
              <Icon name="search" size={16} />
            </span>
            <input
              type="text"
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              placeholder="Search IPs, CIDRs, sandboxes..."
              className="w-full bg-[#F8F6F9] text-[#170B1B] placeholder-[#827787] text-xs pl-9 pr-3 py-1.5 rounded-lg border border-[#E8E0EA] focus:outline-none focus:border-[#2D0035] transition-all"
            />
          </div>
          <button
            onClick={() => {
              setSeverityFilter('ALL')
              setCategoryFilter('ALL')
              setActorFilter('ALL')
              setSearchQuery('')
            }}
            title="Reset Filters"
            className="p-1.5 text-[#827787] hover:text-[#170B1B] rounded-lg hover:bg-[#F8F6F9] border border-[#E8E0EA] transition-colors"
          >
            <Icon name="sync" size={18} />
          </button>
        </div>
      </div>

      {/* Main audit event stream table */}
      <div className="bg-white border border-[#E8E0EA] rounded-xl shadow-xs overflow-hidden flex flex-col">
        {/* Table header status bar */}
        <div className="px-5 py-3 bg-[#F8F6F9] border-b border-[#E8E0EA] flex items-center justify-between text-[#827787] text-xs font-mono">
          <div className="flex items-center gap-2">
            <Icon name="history" size={15} className="text-violet-600" />
            <span>BUFFERED LOG ENTRIES: <strong className="text-[#170B1B]">{filteredRows.length} of 1,428 visible</strong></span>
          </div>
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1 font-mono text-[11px]">
              <span className="w-2 h-2 rounded-full bg-error"></span> 1 Security Denied
            </span>
            <span className="flex items-center gap-1 font-mono text-[11px]">
              <span className="w-2 h-2 rounded-full bg-amber-500"></span> 1 IPAM Cooldown
            </span>
            <span className="flex items-center gap-1 font-mono text-[11px]">
              <span className="w-2 h-2 rounded-full bg-secondary"></span> 3 Operative
            </span>
          </div>
        </div>

        {/* Table structure */}
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="bg-[#F8F6F9] text-[#827787] text-[11px] uppercase tracking-wider font-mono border-b border-[#E8E0EA]">
                <th className="py-3 px-4">Timestamp (UTC)</th>
                <th className="py-3 px-4">Actor</th>
                <th className="py-3 px-4">Action Type</th>
                <th className="py-3 px-4">Target Object</th>
                <th className="py-3 px-4">Severity</th>
                <th className="py-3 px-4">Details &amp; Verdict</th>
                <th className="py-3 px-4 text-right">Inspect</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#E8E0EA]">
              {filteredRows.map(row => {
                const isExpanded = !!expandedRows[row.id]
                const severityBorder = row.severity === 'CRITICAL'
                  ? 'border-l-4 border-l-rose-500'
                  : row.severity === 'WARNING'
                  ? 'border-l-4 border-l-amber-400'
                  : 'border-l-4 border-l-emerald-400'
                return (
                  <tr key={row.id} className="contents">
                    <tr
                      onClick={() => toggleRow(row.id)}
                      className={`hover:bg-[#F8F6F9] transition-colors cursor-pointer ${severityBorder}`}
                    >
                      <td className="py-3.5 px-4 font-mono text-[#5E4D64] whitespace-nowrap">
                        {row.time}
                        <span className="block text-[10px] text-gray-400">{row.timeAgo}</span>
                      </td>
                      <td className="py-3.5 px-4 font-mono whitespace-nowrap">
                        <span className="px-2 py-0.5 rounded bg-[#F8F6F9] border border-[#E8E0EA] text-secondary font-semibold">
                          {row.actor}
                        </span>
                      </td>
                      <td className="py-3.5 px-4 whitespace-nowrap">
                        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-mono font-bold ${
                          row.severity === 'CRITICAL' ? 'bg-rose-100 text-rose-800 border border-rose-200' : row.severity === 'WARNING' ? 'bg-amber-100 text-amber-900 border border-amber-200' : 'bg-[#F8F6F9] border border-[#E8E0EA] text-[#5E4D64]'
                        }`}>
                          {row.action.replace('_', ' ')}
                        </span>
                      </td>
                      <td className="py-3.5 px-4 font-mono whitespace-nowrap text-[#170B1B] font-semibold">
                        {row.target}
                      </td>
                      <td className="py-3.5 px-4 whitespace-nowrap">
                        <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[10px] font-bold ${
                          row.severity === 'CRITICAL' ? 'bg-rose-100 text-rose-800' : row.severity === 'WARNING' ? 'bg-amber-100 text-amber-800' : 'bg-emerald-100 text-emerald-800'
                        }`}>
                          <span className={`w-1.5 h-1.5 rounded-full ${
                            row.severity === 'CRITICAL' ? 'bg-rose-500' : row.severity === 'WARNING' ? 'bg-amber-500' : 'bg-emerald-500'
                          }`} />
                          {row.severity}
                        </span>
                      </td>
                      <td className="py-3.5 px-4 text-[#5E4D64] max-w-md">
                        <span className="line-clamp-1">{row.details}</span>
                      </td>
                      <td className="py-3.5 px-4 text-right">
                        <button
                          onClick={e => { e.stopPropagation(); toggleRow(row.id); }}
                          className="p-1 rounded bg-[#F8F6F9] hover:bg-[#E8E0EA] text-[#170B1B] transition-colors"
                        >
                          <Icon name="alt_route" size={18} className={`transition-transform ${isExpanded ? 'rotate-180' : ''}`} />
                        </button>
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr className="bg-[#F8F6F9]/60">
                        <td colSpan={7} className="p-4 border-l-4 border-secondary">
                          <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-xs font-mono">
                            <div className="p-3 bg-white border border-[#E8E0EA] rounded-lg shadow-xs space-y-1">
                              <span className="text-[#827787] text-[10px] uppercase font-bold">{row.accordion.panel1Title}</span>
                              {row.accordion.panel1Lines.map((l, idx) => (
                                <div key={idx} className="text-[#170B1B]">{l}</div>
                              ))}
                            </div>
                            <div className="p-3 bg-white border border-[#E8E0EA] rounded-lg shadow-xs space-y-1">
                              <span className="text-[#827787] text-[10px] uppercase font-bold">{row.accordion.panel2Title}</span>
                              {row.accordion.panel2Lines.map((l, idx) => (
                                <div key={idx} className="text-[#170B1B]">{l}</div>
                              ))}
                            </div>
                            <div className="p-3 bg-white border border-[#E8E0EA] rounded-lg shadow-xs flex flex-col justify-between gap-2">
                              <div>
                                <span className="text-[#827787] text-[10px] uppercase font-bold">{row.accordion.panel3Title}</span>
                                <p className="font-sans text-xs text-[#4e444d] mt-1">{row.accordion.panel3Desc}</p>
                              </div>
                              {row.accordion.actionBtnText && (
                                <button
                                  onClick={() => showToast('Action Dispatched', `${row.accordion.actionBtnText} initiated via OpenStack control plane.`, 'info')}
                                  className={`px-3 py-1 rounded text-xs font-sans font-semibold self-start ${
                                    row.accordion.actionBtnType === 'error' ? 'bg-error text-white hover:opacity-90' : 'bg-[#2D0035] text-white hover:bg-[#3D0346]'
                                  }`}
                                >
                                  {row.accordion.actionBtnText}
                                </button>
                              )}
                            </div>
                          </div>
                        </td>
                      </tr>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>

        {/* Pagination bar */}
        <div className="px-5 py-3 bg-[#F8F6F9] border-t border-[#E8E0EA] flex items-center justify-between text-xs text-[#827787]">
          <div className="flex items-center gap-2 font-mono">
            <span className="font-semibold text-[#170B1B]">1 - {filteredRows.length}</span>
            <span>of</span>
            <span className="font-semibold text-[#170B1B]">1,428</span>
            <span>audit frames</span>
          </div>
          <div className="flex items-center gap-2 font-mono">
            <button disabled className="px-2.5 py-1 rounded bg-white border border-[#E8E0EA] text-[#827787] opacity-50 cursor-not-allowed">
              Prev
            </button>
            <span className="font-semibold text-[#170B1B]">Page 1 / 286</span>
            <button
              onClick={() => showToast('Telemetry Bus', 'Viewing buffered records from live telemetry stream.', 'info')}
              className="px-2.5 py-1 rounded bg-white border border-[#E8E0EA] hover:bg-slate-50 text-[#170B1B] font-semibold"
            >
              Next
            </button>
          </div>
        </div>
      </div>

      {/* Security drawer sub panel */}
      {showSecurityDrawer && (
        <div className="bg-white border border-[#E8E0EA] rounded-xl shadow-xs p-6 space-y-4">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between pb-4 border-b border-[#E8E0EA] gap-2">
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-rose-600 text-white flex items-center justify-center shadow-xs">
                <Icon name="policy" size={22} />
              </div>
              <div>
                <h2 className="text-base font-bold text-[#170B1B]">Security Policy Blocks &amp; Lateral Drops</h2>
                <span className="text-xs text-[#827787] font-mono">Neutron SG Rule Evaluation &amp; Gateway Port Protections</span>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <span className="px-2.5 py-1 rounded-full bg-rose-50 text-error font-mono text-xs font-semibold border border-rose-200">
                14 Total Blocks in 24h
              </span>
              <button
                onClick={() => setShowSecurityDrawer(false)}
                className="p-1 rounded-lg text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9]"
              >
                <Icon name="close" size={20} />
              </button>
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {/* Panel 1 */}
            <div className="p-4 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-[#170B1B] flex items-center gap-1.5">
                  <Icon name="alt_route" size={16} className="text-rose-600" />
                  Cross-Sandbox Lateral Traffic
                </span>
                <span className="px-2 py-0.5 rounded bg-error text-white font-mono text-[10px] font-bold">11 DROPPED</span>
              </div>
              <p className="text-xs text-[#827787]">
                Attempts by sandboxes to initiate direct point-to-point connections with neighboring sandbox VLAN namespaces without going through approved ingress proxies.
              </p>
              <div className="space-y-2 font-mono text-xs pt-1">
                <div className="p-2.5 bg-white border border-[#E8E0EA] rounded-lg flex items-center justify-between">
                  <div>
                    <div className="font-semibold text-[#170B1B]">sb-analytics → sb-finance-db</div>
                    <span className="text-[10px] text-[#827787]">Rule ID: cstam-iso-strict-91</span>
                  </div>
                  <span className="text-error font-bold">TCP 5432 REJECT</span>
                </div>
                <div className="p-2.5 bg-white border border-[#E8E0EA] rounded-lg flex items-center justify-between">
                  <div>
                    <div className="font-semibold text-[#170B1B]">sb-crawler-02 → sb-internal-auth</div>
                    <span className="text-[10px] text-[#827787]">Rule ID: cstam-iso-strict-91</span>
                  </div>
                  <span className="text-error font-bold">TCP 6379 REJECT</span>
                </div>
              </div>
            </div>

            {/* Panel 2 */}
            <div className="p-4 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-[#170B1B] flex items-center gap-1.5">
                  <Icon name="hub" size={16} className="text-rose-600" />
                  Gateway Infrastructure Management Probes
                </span>
                <span className="px-2 py-0.5 rounded bg-error text-white font-mono text-[10px] font-bold">3 DROPPED</span>
              </div>
              <p className="text-xs text-[#827787]">
                Tenant VMs targeting CSTAM VIP management listeners, OpenStack API daemons, or Keepalived control sockets directly.
              </p>
              <div className="space-y-2 font-mono text-xs pt-1">
                <div className="p-2.5 bg-white border border-[#E8E0EA] rounded-lg flex items-center justify-between">
                  <div>
                    <div className="font-semibold text-[#170B1B]">10.20.30.14 → gw1:8000 (REST API)</div>
                    <span className="text-[10px] text-[#827787]">Rule ID: cstam-sg-isolated-04</span>
                  </div>
                  <span className="text-error font-bold">DROP_SILENT</span>
                </div>
                <div className="p-2.5 bg-white border border-[#E8E0EA] rounded-lg flex items-center justify-between">
                  <div>
                    <div className="font-semibold text-[#170B1B]">10.20.30.42 → gw1:22 (SSH Probe)</div>
                    <span className="text-[10px] text-[#827787]">Rule ID: cstam-mgmt-deny-all</span>
                  </div>
                  <span className="text-error font-bold">TCP RST</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
