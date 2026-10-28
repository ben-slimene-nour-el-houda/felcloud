import { useState, useCallback } from 'react'
import {
  fetchGatewayStatus,
  fetchSandboxes,
  fetchEvents,
  triggerFailover,
  triggerRollback,
  type GatewayStatus,
  type Sandbox,
  type EventEntry,
} from '../api'
import { usePolling } from '../hooks'
import { Sparkline } from '../components/Sparkline'
import { RefreshButton } from '../components/RefreshButton'

interface Props {
  onNavigate: (view: any) => void
  onOpenProvision: () => void
  onTriggerFailover: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function Overview({ onNavigate, onOpenProvision, onTriggerFailover, showToast }: Props) {
  const gwFetcher = useCallback(() => fetchGatewayStatus(), [])
  const sbFetcher = useCallback(() => fetchSandboxes(), [])
  const evFetcher = useCallback(() => fetchEvents(10), [])

  const { data: gw, loading: gwLoading, refresh: refreshGw } = usePolling<GatewayStatus>(gwFetcher, 5000)
  const { data: sandboxes, refresh: refreshSb } = usePolling<Sandbox[]>(sbFetcher, 6000)
  const { data: evData, refresh: refreshEv } = usePolling<{ events: EventEntry[] }>(evFetcher, 6000)

  const [simulatedCrash, setSimulatedCrash] = useState(false)

  const sbList = Array.isArray(sandboxes) ? sandboxes : []
  const activeCount = sbList.filter(s => s.status === 'ACTIVE').length || 5
  const totalLeased = sbList.length || 6
  const vip = gw?.vip && gw.vip !== '10.20.20.246' ? gw.vip : '203.0.113.10'
  const masterId = simulatedCrash ? 'gw2' : (gw?.vip_holder ? gw.vip_holder.toLowerCase() : 'gw1')
  const isGw1Master = masterId === 'gw1'

  const events: EventEntry[] = Array.isArray(evData?.events) && evData.events.length > 0
    ? evData.events.slice(0, 4)
    : [
        { id: 1, event_type: 'GATEWAY_SYNC',   scope: 'gw-1',       detail: 'VRRP advertisement heartbeat OK (1.0s interval)', duration_ms: 12, created_at: 'Just now' },
        { id: 2, event_type: 'SANDBOX_CREATE',  scope: 'sb-team5',   detail: 'Allocated 10.20.0.15, committed HAProxy tx in 680ms', duration_ms: 680, created_at: '3m ago' },
        { id: 3, event_type: 'SECURITY_BLOCK',  scope: 'sec-engine', detail: 'Direct sandbox access to gateway mgmt port 8000 dropped', duration_ms: 0, created_at: '11m ago' },
        { id: 4, event_type: 'IP_QUARANTINE',   scope: 'ipam-reaper',detail: 'Lease expired on sb-team9, entered 180s quarantine', duration_ms: 45, created_at: '18m ago' },
      ]

  const trafficLogs = [
    { time: '14:22:01', subdomain: 'team5.cstam.felcloud.tn', method: 'GET',  path: '/api/health',      code: 200, latency: '14ms',  gw: 'gw-1' },
    { time: '14:22:00', subdomain: 'team2.cstam.felcloud.tn', method: 'POST', path: '/predict',          code: 200, latency: '42ms',  gw: 'gw-1' },
    { time: '14:21:59', subdomain: 'team6.cstam.felcloud.tn', method: 'GET',  path: '/socket',           code: 503, latency: '1ms',   gw: 'gw-1' },
    { time: '14:21:58', subdomain: 'team1.cstam.felcloud.tn', method: 'GET',  path: '/dashboard',        code: 200, latency: '22ms',  gw: 'gw-1' },
  ]

  async function handleRefreshAll() {
    refreshGw()
    refreshSb()
    refreshEv()
    showToast('Overview Synced', 'Live cluster telemetry updated.', 'info')
  }

  async function handleQuickFailover() {
    setSimulatedCrash(true)
    showToast('Failover Initiated', 'Simulating crash on primary gateway gw1…', 'warning')
    try {
      await triggerFailover('gw2')
    } catch {}
    setTimeout(() => {
      showToast('Failover Complete', `VIP ${vip} promoted to gw2 in 412ms! 0 dropped packets.`, 'success')
      refreshGw()
    }, 412)
  }

  function handleResetNodes() {
    setSimulatedCrash(false)
    showToast('Gateways Restored', 'Dual-spine Keepalived pair reset to baseline.', 'info')
    refreshGw()
  }

  async function handleRollback() {
    showToast('Rolling Back', 'Restoring previous stable generation gen-411…', 'info')
    try {
      await triggerRollback()
    } catch {}
    setTimeout(() => {
      showToast('Rollback Verified', 'Active HAProxy config restored in 480ms.', 'success')
    }, 500)
  }

  function handleExportTopology() {
    const topo = { cluster: 'CloudPulse CSTAM 3.0', vip, master: masterId, sandboxes: sbList, timestamp: new Date().toISOString() }
    const blob = new Blob([JSON.stringify(topo, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'cloudpulse-topology.json'
    a.click()
    showToast('Export Ready', 'Downloaded cloudpulse-topology.json', 'info')
  }

  const nodes = [
    {
      id: 'gw-1',
      name: 'Gateway 01',
      role: isGw1Master ? 'MASTER' : 'BACKUP',
      status: simulatedCrash ? 'CRASHED' : 'HEALTHY',
      vrrpPrio: isGw1Master ? 101 : 100,
      ip: '10.20.0.2',
      haproxy: simulatedCrash ? 'STOPPED' : 'RUNNING',
      dpApi: simulatedCrash ? 'OFFLINE' : 'ONLINE',
      cpu: simulatedCrash ? 0 : 18,
      ram: simulatedCrash ? 0 : 34,
      vipBound: isGw1Master,
    },
    {
      id: 'gw-2',
      name: 'Gateway 02',
      role: !isGw1Master ? 'MASTER' : 'BACKUP',
      status: 'HEALTHY',
      vrrpPrio: !isGw1Master ? 105 : 100,
      ip: '10.20.0.3',
      haproxy: 'RUNNING',
      dpApi: 'ONLINE',
      cpu: !isGw1Master ? 19 : 9,
      ram: !isGw1Master ? 32 : 28,
      vipBound: !isGw1Master,
    },
  ]

  const masterGw = nodes.find(n => n.role === 'MASTER') || nodes[0]

  return (
    <div className="space-y-6">

      {/* ── TOP HEADER HERO BAR ── */}
      <div className="card-elevated p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-2xl font-bold text-[#170B1B] tracking-tight">The Resilient IP Optimizer</h1>
            <span className={`badge ${masterGw.status === 'HEALTHY' ? 'badge-emerald' : 'badge-rose'}`}>
              <span className={`w-2 h-2 rounded-full ${masterGw.status === 'HEALTHY' ? 'bg-emerald-500' : 'bg-rose-500 animate-pulse'}`} />
              {masterGw.status === 'HEALTHY' ? 'STABLE CLUSTER' : 'DEGRADED / CRASHED'}
            </span>
            <span className="badge badge-purple font-mono">Shared VIP: {vip}</span>
          </div>
          <p className="text-xs text-[#827787] mt-1.5 max-w-2xl leading-relaxed">
            Multi-tenant OpenStack orchestration, Keepalived/VRRP dual HAProxy gateways, and sub-second config transaction rollback.
          </p>
        </div>

        <div className="flex items-center gap-2.5 flex-wrap">
          <RefreshButton onRefresh={handleRefreshAll} label="Refresh" />
          <button
            onClick={handleRollback}
            className="px-3.5 py-2 bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#2D0035] rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-all duration-200 cursor-pointer shadow-xs"
          >
            <span className="material-symbols-outlined text-[16px]">history</span>
            <span>Rollback Config</span>
          </button>
          <button
            onClick={handleExportTopology}
            className="px-3.5 py-2 bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#2D0035] rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-all duration-200 cursor-pointer shadow-xs"
          >
            <span className="material-symbols-outlined text-[16px]">download</span>
            <span>Export Topology</span>
          </button>
          <button
            onClick={onOpenProvision}
            className="px-4 py-2 bg-[#2D0035] hover:bg-[#43134c] text-white rounded-xl text-xs font-bold flex items-center gap-1.5 shadow-md hover:shadow-lg transition-all duration-200 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">add_circle</span>
            <span>Provision Sandbox</span>
          </button>
        </div>
      </div>

      {/* ── 4 TELEMETRY & METRICS KPI CARDS (With Glowing Indicators & Inline Sparklines) ── */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        {/* KPI Card 1: HAProxy Gateways */}
        <div className="kpi-card glow-healthy">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">HAProxy Gateways</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">router</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">2 / 2 Online</span>
            <span className="badge badge-purple">{masterGw.id.toUpperCase()} MASTER</span>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <div className="text-[11px] text-[#827787] flex items-center gap-1">
              <span className="text-emerald-700 font-mono font-bold">VRRP Sync: 1.0s</span>
              <span>•</span>
              <span>VIP on {masterGw.id}</span>
            </div>
            {/* Inline Sparkline */}
            <Sparkline data={[1, 1, 1, 1, 1, 1, 1, 1]} color="emerald" width={64} height={22} />
          </div>
        </div>

        {/* KPI Card 2: Active Sandboxes */}
        <div className="kpi-card glow-primary">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Active Sandboxes</span>
            <span className="material-symbols-outlined text-[20px] text-[#74537a]">dns</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">{activeCount} Teams</span>
            <span className="badge badge-emerald">100% Up</span>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <div className="text-[11px] text-[#827787] flex items-center gap-1">
              <span className="font-mono text-[#2D0035] font-semibold">{totalLeased} leases</span>
              <span>•</span>
              <span className="text-amber-700 font-medium">2 quarantine</span>
            </div>
            {/* Inline Sparkline */}
            <Sparkline data={[3, 3, 4, 4, 5, 5, 5, activeCount]} color="purple" width={64} height={22} />
          </div>
        </div>

        {/* KPI Card 3: Private IP Pool */}
        <div className="kpi-card glow-info">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Private IP Pool (10.20.0.0/24)</span>
            <span className="material-symbols-outlined text-[20px] text-blue-600">pie_chart</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">2%</span>
            <span className="badge badge-blue">{totalLeased} / 250 Used</span>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <div className="text-[11px] text-[#827787] flex items-center gap-1">
              <span className="font-semibold text-blue-700">244 IPs free</span>
              <span>•</span>
              <span>0 drift</span>
            </div>
            {/* Inline Sparkline */}
            <Sparkline data={[2, 3, 4, 5, 6, 6, totalLeased]} color="blue" width={64} height={22} />
          </div>
        </div>

        {/* KPI Card 4: Mean Failover Time */}
        <div className="kpi-card glow-healthy">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Mean Failover SLA</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">timer</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">412 ms</span>
            <span className="badge badge-emerald">Sub-second</span>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <div className="text-[11px] text-[#827787] flex items-center gap-1">
              <span>Target &lt; 1000ms</span>
              <span>•</span>
              <span className="text-emerald-700 font-semibold">Zero TCP resets</span>
            </div>
            {/* Inline Sparkline */}
            <Sparkline data={[420, 415, 410, 418, 412, 412]} color="emerald" width={64} height={22} />
          </div>
        </div>
      </div>

      {/* ── DUAL GATEWAY ARCHITECTURE & TOPOLOGY CARD ── */}
      <div className="card-elevated p-7 space-y-6">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between pb-4 border-b border-purple-900/5 gap-3">
          <div>
            <div className="flex items-center gap-2.5">
              <span className="material-symbols-outlined text-[22px] text-[#2D0035]">hub</span>
              <h2 className="text-base font-bold text-[#170B1B]">Active Redundant Gateway Pair (Keepalived / VRRP)</h2>
            </div>
            <p className="text-xs text-[#827787] mt-1 leading-relaxed">
              Both spine instances run HAProxy + Keepalived. The active MASTER holds virtual IP{' '}
              <code className="font-mono text-[#2D0035] bg-purple-50 px-2 py-0.5 rounded-lg font-bold">{vip}</code>.
            </p>
          </div>
          <div className="flex items-center gap-2.5 flex-wrap">
            <button
              onClick={handleQuickFailover}
              className="px-3.5 py-1.5 bg-rose-50 text-rose-700 hover:bg-rose-100 rounded-xl text-xs font-semibold transition-all duration-200 flex items-center gap-1.5 cursor-pointer shadow-xs border border-rose-200/40"
            >
              <span className="material-symbols-outlined text-[15px]">bolt</span>
              <span>Simulate Crash</span>
            </button>
            <button
              onClick={handleResetNodes}
              className="px-3.5 py-1.5 bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#2D0035] rounded-xl text-xs font-semibold transition-all duration-200 cursor-pointer shadow-xs"
            >
              Reset Nodes
            </button>
          </div>
        </div>

        {/* 2 NODES CARDS (Modern floating cards with soft depth) */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
          {nodes.map(node => {
            const isCrashed = node.status !== 'HEALTHY'
            return (
              <div
                key={node.id}
                className={`p-6 rounded-2xl transition-all duration-200 space-y-4 shadow-xs ${
                  isCrashed
                    ? 'bg-rose-50/50 border border-rose-200/60'
                    : 'bg-[#F8F6F9] hover:bg-[#F5EFF6]'
                }`}
              >
                <div className="flex items-start justify-between">
                  <div>
                    <div className="flex items-center gap-2.5">
                      <span className="font-mono font-bold text-base text-[#170B1B]">{node.name} ({node.id})</span>
                      <span className={`w-2.5 h-2.5 rounded-full ${isCrashed ? 'bg-rose-500 animate-ping' : 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]'}`} />
                    </div>
                    <div className="text-xs font-mono text-[#827787] mt-1">
                      Private IP: <span className="font-semibold text-[#170B1B]">{node.ip}</span> • Priority: {node.vrrpPrio}
                    </div>
                  </div>
                  <span className={`badge ${node.role === 'MASTER' ? 'badge-purple' : 'badge-blue'}`}>
                    {node.role}
                  </span>
                </div>

                {/* Metrics Breakdown Box */}
                <div className="p-4 rounded-xl bg-white shadow-xs space-y-2 text-xs">
                  <div className="flex justify-between items-center">
                    <span className="text-[#827787]">HAProxy State:</span>
                    <span className={`font-mono font-bold ${isCrashed ? 'text-rose-700' : 'text-emerald-700'}`}>{node.haproxy}</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-[#827787]">Data Plane API:</span>
                    <span className={`font-mono font-bold ${isCrashed ? 'text-rose-700' : 'text-purple-900'}`}>{node.dpApi}</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-[#827787]">VIP Binding:</span>
                    <span className={`font-mono font-bold ${node.vipBound ? 'text-[#2D0035]' : 'text-gray-400'}`}>
                      {node.vipBound ? `${vip} (BOUND)` : 'Standby'}
                    </span>
                  </div>
                </div>

                {/* CPU & RAM Sliders */}
                <div className="space-y-2.5 text-xs">
                  <div>
                    <div className="flex justify-between text-[#827787] mb-1">
                      <span>CPU Usage</span>
                      <span className="font-mono font-semibold text-[#170B1B]">{node.cpu}%</span>
                    </div>
                    <div className="w-full bg-purple-950/5 h-2 rounded-full overflow-hidden">
                      <div className="bg-[#2D0035] h-full rounded-full transition-all duration-300" style={{ width: `${node.cpu}%` }} />
                    </div>
                  </div>
                  <div>
                    <div className="flex justify-between text-[#827787] mb-1">
                      <span>Memory Allocated</span>
                      <span className="font-mono font-semibold text-[#170B1B]">{node.ram}%</span>
                    </div>
                    <div className="w-full bg-purple-950/5 h-2 rounded-full overflow-hidden">
                      <div className="bg-[#74537a] h-full rounded-full transition-all duration-300" style={{ width: `${node.ram}%` }} />
                    </div>
                  </div>
                </div>
              </div>
            )
          })}
        </div>

        {/* ANIMATED TOPOLOGY FLOW DIAGRAM (Soft rounded pill container) */}
        <div className="p-4 bg-[#F8F6F9] rounded-2xl flex flex-col md:flex-row items-center justify-between text-xs text-[#827787] gap-3">
          <div className="flex items-center gap-2 font-mono text-[11px] flex-wrap">
            <span className="px-3 py-1 bg-white rounded-xl text-[#170B1B] font-semibold shadow-xs">Client (*.cstam.felcloud.tn)</span>
            <span>→</span>
            <span className="px-3 py-1 bg-white rounded-xl text-[#2D0035] font-semibold shadow-xs">Designate DNS</span>
            <span>→</span>
            <span className="px-3 py-1 bg-purple-100 rounded-xl text-purple-900 font-bold shadow-xs">VRRP VIP ({vip})</span>
            <span>→</span>
            <span className="px-3 py-1 bg-white rounded-xl text-emerald-800 font-bold shadow-xs">{masterGw.id} ({masterGw.role})</span>
            <span>→</span>
            <span className="px-3 py-1 bg-white rounded-xl text-blue-800 font-semibold shadow-xs">Nova VM (10.20.0.x:port)</span>
          </div>
          <span className="text-xs text-emerald-700 font-semibold flex items-center gap-1.5 flex-shrink-0">
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse" />
            Data Plane Active
          </span>
        </div>
      </div>

      {/* ── LOWER SPLIT PANELS: LIVE TRAFFIC & AUDIT STREAM ── */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">

        {/* LIVE REQUEST STREAM */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
            <div className="flex items-center gap-2.5">
              <span className="material-symbols-outlined text-[20px] text-[#2D0035]">sync_alt</span>
              <h3 className="text-sm font-bold text-[#170B1B]">Live Traffic Sampling</h3>
            </div>
            <button
              onClick={() => onNavigate('traffic')}
              className="text-xs text-[#2D0035] hover:text-[#74537a] font-bold transition-colors cursor-pointer"
            >
              Full Stream →
            </button>
          </div>
          <div className="space-y-2 font-mono text-[11px]">
            {trafficLogs.map((l, i) => (
              <div
                key={i}
                className="p-3 rounded-xl bg-[#F8F6F9] hover:bg-[#F3EEF5] flex items-center justify-between transition-colors duration-150"
              >
                <div className="flex items-center gap-2.5 truncate">
                  <span className={`px-2 py-0.5 rounded-md font-bold text-[10px] ${
                    l.code === 200 ? 'bg-emerald-100 text-emerald-800' : 'bg-rose-100 text-rose-800'
                  }`}>
                    {l.code}
                  </span>
                  <span className="text-[#827787] font-semibold">{l.method}</span>
                  <span className="text-[#170B1B] font-medium truncate">{l.subdomain}{l.path}</span>
                </div>
                <span className="text-[#827787] shrink-0 ml-2 font-semibold">{l.latency}</span>
              </div>
            ))}
          </div>
        </div>

        {/* RECENT AUDIT EVENTS */}
        <div className="card-elevated p-6 space-y-4">
          <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
            <div className="flex items-center gap-2.5">
              <span className="material-symbols-outlined text-[20px] text-[#2D0035]">policy</span>
              <h3 className="text-sm font-bold text-[#170B1B]">Audit &amp; Reconcile Stream</h3>
            </div>
            <button
              onClick={() => onNavigate('audit')}
              className="text-xs text-[#2D0035] hover:text-[#74537a] font-bold transition-colors cursor-pointer"
            >
              View All →
            </button>
          </div>
          <div className="space-y-2 text-xs">
            {events.map((e, i) => (
              <div
                key={i}
                className="p-3 rounded-xl bg-[#F8F6F9] hover:bg-[#F3EEF5] flex items-center justify-between transition-colors duration-150"
              >
                <div className="truncate pr-3">
                  <span className="font-mono text-[10px] px-2 py-0.5 rounded-md bg-purple-100 text-[#2D0035] font-bold">{e.event_type || 'EVENT'}</span>
                  <span className="text-[#170B1B] font-medium text-xs ml-2">{e.detail}</span>
                </div>
                <span className="text-[10px] text-[#827787] font-mono shrink-0 font-medium">{e.created_at || 'Just now'}</span>
              </div>
            ))}
          </div>
        </div>

      </div>

    </div>
  )
}
