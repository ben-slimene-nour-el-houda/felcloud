import { useState, useCallback } from 'react'
import { fetchEvents, type EventEntry } from '../api'
import { usePolling } from '../hooks'
import { Sparkline } from '../components/Sparkline'
import { RefreshButton } from '../components/RefreshButton'

interface Props {
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

export default function Events({ showToast }: Props) {
  const evFetcher = useCallback(() => fetchEvents(100), [])
  const { data: evData, refresh } = usePolling<{ events: EventEntry[] }>(evFetcher, 6000)

  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})
  const [showSecurityDrawer, setShowSecurityDrawer] = useState(false)
  const [range, setRange] = useState<'1h' | '24h' | '7d'>('24h')
  const [severityFilter, setSeverityFilter] = useState('ALL')
  const [categoryFilter, setCategoryFilter] = useState('ALL')
  const [actorFilter, setActorFilter] = useState('ALL')
  const [searchQuery, setSearchQuery] = useState('')

  const actors = ['ALL', 's.chen@eng', 'neutron-agent', 'openstack-reaper-bot', 'vrrp-monitor']

  const defaultRows: AuditRow[] = [
    {
      id: 'row-1',
      time: '14:28:02.194',
      timeAgo: 'today • 2m ago',
      actor: 'neutron-agent',
      action: 'SECURITY_BLOCK',
      target: 'sb-analytics → gw1:8000',
      severity: 'CRITICAL',
      category: 'SECURITY',
      details: 'Sandbox sb-analytics attempted access to gateway mgmt port 8000 (Blocked by Neutron SG rule cstam-sg-isolated-04).',
      accordion: {
        panel1Title: 'PACKET METRICS & HEADERS',
        panel1Lines: ['SRC: 10.20.10.14:48292 [tap-sb-anl]', 'DST: 10.20.10.1:8000 (MGMT_VLAN)', 'PROTOCOL: TCP SYN | FLAG: 0x002'],
        panel2Title: 'FIREWALL ENFORCEMENT',
        panel2Lines: ['RULE-ID: cstam-sg-isolated-04', 'DISPOSITION: REJECT_SILENT (DROP)', 'HOOK: ovs-neutron-firewall-v2'],
        panel3Title: 'ACTION RECOMMENDED',
        panel3Desc: 'Automated quarantine audit flag initiated. Host sandbox sb-analytics flagged for lateral recon probe.',
        actionBtnText: 'Quarantine Sandbox',
        actionBtnType: 'error',
      },
    },
    {
      id: 'row-2',
      time: '14:21:44.802',
      timeAgo: 'today • 8m ago',
      actor: 'openstack-reaper-bot',
      action: 'IPAM_QUARANTINE',
      target: '10.20.10.88',
      severity: 'WARNING',
      category: 'IPAM',
      details: 'IP 10.20.10.88 moved to quarantine cooldown (180s timer) after sandbox deletion.',
      accordion: {
        panel1Title: 'IPAM POOL LIFECYCLE',
        panel1Lines: ['ORIGIN: sb-ephemeral-test-841', 'COOLDOWN EXPIRES: 14:26:44 UTC', 'PURGE ARP CACHE: Verified Success'],
        panel2Title: 'ROUTER STATE',
        panel2Lines: ['SUBNET: pool-west-cstam-24', 'CLEANSE SCRIPT: ipam_sanitize.py#88', 'CONNTRACK FLUSH: 42 entries wiped'],
        panel3Title: 'OVERRIDE ACTION',
        panel3Desc: 'Force release IP back into available alloc pool immediately without waiting out the cooldown timer.',
        actionBtnText: 'Force Release Early',
        actionBtnType: 'default',
      },
    },
    {
      id: 'row-3',
      time: '13:59:10.012',
      timeAgo: 'today • 31m ago',
      actor: 'vrrp-monitor',
      action: 'GATEWAY_VRRP',
      target: 'gw1 (VIP 203.0.113.10)',
      severity: 'INFO',
      category: 'VRRP',
      details: 'Heartbeat sync verified on eth0.50; gw1 affirmed as MASTER (Priority 101 vs 100).',
      accordion: {
        panel1Title: 'KEEPALIVED HEARTBEAT TELEMETRY',
        panel1Lines: ['VIRTUAL ROUTER ID (VRID): 51', 'STATE: MASTER [gw1]', 'BACKUP NODE: gw2 (Standby Healthy)'],
        panel2Title: 'ADVERTISEMENT STATUS',
        panel2Lines: ['INTERVAL: 1000ms ± 4ms', 'PACKET LOSS: 0.000% (Last 10k frames)', 'VIP ARPING: Broadcasted Gratuitous ARP'],
        panel3Title: 'DRILL READINESS',
        panel3Desc: 'Failover simulated safely during morning readiness audit with zero dropped active TCP sessions.',
      },
    },
    {
      id: 'row-4',
      time: '12:44:22.510',
      timeAgo: 'today • 1h 45m ago',
      actor: 's.chen@eng',
      action: 'CONFIG_COMMIT',
      target: 'gen-412',
      severity: 'INFO',
      category: 'CONFIG',
      details: 'Generation gen-412 validated and successfully applied across 2 gateway nodes (Checksum: 8fa9c1).',
      accordion: {
        panel1Title: 'GENERATION SPECIFICATIONS',
        panel1Lines: ['GENERATION: gen-412', 'AUTHOR: s.chen@eng (Staff NetArch)', 'DIFF: +4 firewall rules, -1 route override'],
        panel2Title: 'DISTRIBUTED RECONCILIATION',
        panel2Lines: ['gw1: RECONCILED (0.8s)', 'gw2: RECONCILED (1.1s)', 'VALIDATION: Dry-run passed without warnings'],
        panel3Title: 'ROLLBACK SNAPSHOT',
        panel3Desc: 'Previous stable snapshot gen-411 stored in immutable etcd ledger with 24-hour instant revert lock.',
        actionBtnText: 'View Schema Diff',
        actionBtnType: 'default',
      },
    },
    {
      id: 'row-5',
      time: '11:15:08.771',
      timeAgo: 'today • 3h 14m ago',
      actor: 's.chen@eng',
      action: 'SANDBOX_CREATE',
      target: 'team4-vm-alpha',
      severity: 'INFO',
      category: 'SANDBOX',
      details: 'Provisioned team4-vm-alpha (tap-t4-01, IP 10.20.10.19) in 4.2s.',
      accordion: {
        panel1Title: 'ORCHESTRATION PIPELINE',
        panel1Lines: ['FLAVOR: cstam.standard.4c8g', 'TAP INTERFACE: tap-t4-01 (veth pair bound)', 'BOOT DURATION: 4,210 ms total'],
        panel2Title: 'SECURITY ASSIGNMENTS',
        panel2Lines: ['SECURITY GROUP: sg-team4-default', 'ISOLATION: Strict eBPF host shield', 'EGRESS POLICY: Default gateway NAT'],
        panel3Title: 'METADATA AUDIT',
        panel3Desc: 'Instance assigned to Project ID prj-sandbox-redteam with standard ephemerality flag.',
      },
    },
  ]

  // Filter rows
  const filteredRows = defaultRows.filter(r => {
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
    showToast('Exporting CSTAM Audit Stream', '1,428 records exporting in CSV format...', 'info')
  }

  function cycleActor() {
    const idx = actors.indexOf(actorFilter)
    const next = actors[(idx + 1) % actors.length]
    setActorFilter(next)
  }

  return (
    <div className="space-y-6 min-w-0">
      {/* Header section */}
      <div className="card-elevated p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <span className="badge badge-purple font-mono">CLUSTER AUDIT TRAIL</span>
            <span className="text-xs font-mono text-[#827787]">STREAM: cstam-w-sec-092</span>
          </div>
          <h1 className="text-2xl font-bold text-[#170B1B] tracking-tight mt-1">Cluster Security &amp; Operations Audit Stream</h1>
          <p className="text-xs text-[#827787] mt-1.5 max-w-3xl leading-relaxed">
            Deterministic immutable log of hypervisor events, network mutations, VRRP state-shifts, and eBPF/Neutron policy enforcement.
          </p>
        </div>
        <div className="flex items-center gap-2.5 flex-wrap">
          <RefreshButton onRefresh={refresh} label="Refresh" />
          <button
            onClick={() => setShowSecurityDrawer(!showSecurityDrawer)}
            className="px-4 py-2 rounded-xl bg-rose-50 text-rose-700 hover:bg-rose-100 text-xs font-semibold flex items-center gap-2 transition-all duration-200 cursor-pointer shadow-xs border border-rose-200/50"
          >
            <span className="material-symbols-outlined text-[16px]">shield</span>
            <span>Security Summary</span>
            <span className="px-2 py-0.5 bg-rose-600 text-white rounded-full font-mono text-[10px] font-bold">14 Blocked</span>
          </button>
          <button
            onClick={handleExportCsv}
            className="px-4 py-2 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] text-xs font-semibold flex items-center gap-2 transition-all duration-200 cursor-pointer shadow-xs"
          >
            <span className="material-symbols-outlined text-[16px]">file_download</span>
            <span>Export CSV</span>
          </button>
        </div>
      </div>

      {/* KPI metric cards grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        {/* Card 1: Total Events */}
        <div className="kpi-card glow-primary">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">TOTAL EVENTS (24H)</span>
            <span className="material-symbols-outlined text-[20px] text-[#74537a]">receipt_long</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">1,428</span>
            <span className="badge badge-purple">+12.4% yday</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>Normal cadence</span>
            <Sparkline data={[1100, 1250, 1300, 1428]} color="purple" width={48} height={16} />
          </div>
        </div>

        {/* Card 2: Security Denials */}
        <div className="kpi-card glow-danger">
          <div className="flex items-center justify-between text-rose-700 text-xs">
            <span className="font-semibold tracking-wide">SECURITY DENIALS</span>
            <span className="material-symbols-outlined text-[20px] text-rose-600">gpp_bad</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-rose-700 font-mono">14</span>
            <span className="badge badge-rose">Blocked</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span className="text-rose-700 font-semibold">100% Mitigated</span>
            <Sparkline data={[2, 4, 8, 12, 14]} color="rose" width={48} height={16} />
          </div>
        </div>

        {/* Card 3: Gateway VRRP Shifts */}
        <div className="kpi-card glow-healthy">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">GATEWAY STATE SHIFTS</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">swap_horiz</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">2</span>
            <span className="badge badge-emerald">VRRP Sync OK</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>VIP on gw1 (MASTER)</span>
            <Sparkline data={[1, 1, 2, 2, 2]} color="emerald" width={48} height={16} />
          </div>
        </div>

        {/* Card 4: IPAM Lifecycle */}
        <div className="kpi-card glow-info">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">IPAM LIFECYCLE</span>
            <span className="material-symbols-outlined text-[20px] text-blue-600">lan</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">68</span>
            <span className="badge badge-blue">Allocations</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>2 in cooldown</span>
            <Sparkline data={[45, 52, 60, 68]} color="blue" width={48} height={16} />
          </div>
        </div>
      </div>

      {/* Filter Bar */}
      <div className="card-elevated p-4 flex flex-col lg:flex-row items-stretch lg:items-center justify-between gap-3.5">
        <div className="flex flex-wrap items-center gap-2">
          {/* Range pills */}
          <div className="inline-flex p-1 bg-[#F8F6F9] rounded-xl text-xs font-mono">
            {(['1h', '24h', '7d'] as const).map(r => (
              <button
                key={r}
                onClick={() => setRange(r)}
                className={`px-3 py-1 rounded-lg transition-all duration-200 cursor-pointer ${
                  range === r ? 'bg-white text-[#170B1B] shadow-xs font-bold' : 'text-[#827787] hover:text-[#170B1B]'
                }`}
              >
                {r}
              </button>
            ))}
          </div>

          {/* Severity selector */}
          <select
            value={severityFilter}
            onChange={e => setSeverityFilter(e.target.value)}
            className="bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] rounded-xl px-3 py-1.5 text-xs font-semibold cursor-pointer focus:outline-none transition-all"
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
            className="bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] rounded-xl px-3 py-1.5 text-xs font-semibold cursor-pointer focus:outline-none transition-all"
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
            className="px-3.5 py-1.5 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer"
          >
            <span className="material-symbols-outlined text-[15px] text-[#74537a]">account_circle</span>
            <span>Actor: {actorFilter}</span>
          </button>
        </div>

        {/* Search */}
        <div className="relative min-w-[240px]">
          <span className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-[#827787]">
            <span className="material-symbols-outlined text-[17px]">search</span>
          </span>
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search audit trail by actor, IP, or type…"
            className="w-full pl-9 pr-3.5 py-1.5 bg-[#F8F6F9] focus:bg-white text-xs rounded-xl text-[#170B1B] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 transition-all shadow-xs"
          />
        </div>
      </div>

      {/* Audit Log Table (Spacious table-modern) */}
      <div className="card-elevated overflow-hidden">
        <div className="overflow-x-auto">
          <table className="table-modern">
            <thead>
              <tr>
                <th>Timestamp</th>
                <th>Actor</th>
                <th>Action &amp; Target</th>
                <th>Severity</th>
                <th>Details</th>
                <th className="text-right">Expand</th>
              </tr>
            </thead>
            <tbody>
              {filteredRows.map(row => {
                const isExpanded = !!expandedRows[row.id]
                return (
                  <tr key={row.id} className="cursor-pointer" onClick={() => toggleRow(row.id)}>
                    <td className="whitespace-nowrap font-mono text-xs text-[#827787]">
                      <div className="font-semibold text-[#170B1B]">{row.time}</div>
                      <div className="text-[10px] text-[#827787]">{row.timeAgo}</div>
                    </td>
                    <td className="font-mono text-xs font-semibold text-[#74537a] whitespace-nowrap">
                      {row.actor}
                    </td>
                    <td>
                      <div className="font-bold text-[#170B1B] text-xs">{row.action}</div>
                      <div className="font-mono text-[11px] text-[#827787] mt-0.5">{row.target}</div>
                    </td>
                    <td className="whitespace-nowrap">
                      <span className={`badge ${
                        row.severity === 'CRITICAL' ? 'badge-rose' : row.severity === 'WARNING' ? 'badge-amber' : 'badge-emerald'
                      }`}>
                        {row.severity}
                      </span>
                    </td>
                    <td className="text-xs text-[#170B1B] max-w-md truncate">
                      {row.details}
                    </td>
                    <td className="text-right whitespace-nowrap">
                      <span className={`material-symbols-outlined text-[18px] text-[#827787] transition-transform duration-200 ${isExpanded ? 'rotate-180' : ''}`}>
                        expand_more
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Security Drawer Modal */}
      {showSecurityDrawer && (
        <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm animate-fade-in" onClick={() => setShowSecurityDrawer(false)}>
          <div
            className="absolute right-0 top-0 h-full w-[480px] bg-white rounded-l-3xl shadow-2xl p-7 overflow-y-auto space-y-6"
            onClick={e => e.stopPropagation()}
          >
            <div className="flex items-center justify-between pb-3.5 border-b border-purple-900/5">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-2xl bg-rose-50 flex items-center justify-center text-rose-600">
                  <span className="material-symbols-outlined text-[20px]">shield</span>
                </div>
                <div>
                  <h3 className="font-bold text-base text-[#170B1B]">Cluster Security Audit</h3>
                  <p className="text-xs text-[#827787]">eBPF &amp; Neutron Firewall Traps</p>
                </div>
              </div>
              <button
                onClick={() => setShowSecurityDrawer(false)}
                className="w-8 h-8 rounded-xl text-[#827787] hover:text-[#170B1B] hover:bg-[#F5F3F7] flex items-center justify-center cursor-pointer"
              >
                <span className="material-symbols-outlined text-[18px]">close</span>
              </button>
            </div>

            <div className="space-y-3">
              <div className="p-4 rounded-2xl bg-rose-50/70 border border-rose-200/50 space-y-2">
                <span className="badge badge-rose">Active Enforcement</span>
                <div className="font-bold text-rose-900 text-xs">Port 8000 Scan Drops: 14 Events</div>
                <p className="text-xs text-rose-800 leading-relaxed">
                  Tenant sandboxes blocked from probing HAProxy management port 8000 and internal control plane RPC endpoints.
                </p>
              </div>

              <div className="p-4 rounded-2xl bg-[#F8F6F9] space-y-2 text-xs">
                <div className="font-bold text-[#170B1B]">Security Group Policies Active</div>
                <div className="flex justify-between text-[#827787]">
                  <span>cstam-sg-isolated-04:</span>
                  <span className="font-mono font-bold text-emerald-700">ENFORCED</span>
                </div>
                <div className="flex justify-between text-[#827787]">
                  <span>Anti-Spoof ARP Shield:</span>
                  <span className="font-mono font-bold text-emerald-700">ENABLED</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
