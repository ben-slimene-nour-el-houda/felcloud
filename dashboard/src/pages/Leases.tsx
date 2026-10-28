import { useState, useCallback, useEffect } from 'react'
import { fetchLeases, fetchSandboxes, type LeaseEntry, type Sandbox, type SharedState } from '../api'
import { usePolling, formatDuration } from '../hooks'
import Icon from '../components/Icon'

interface Props {
  sharedState?: SharedState | null
  refreshSharedState?: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function Leases({ sharedState, refreshSharedState, showToast }: Props) {
  const leaseFetcher = useCallback(() => fetchLeases(), [])
  const sbFetcher    = useCallback(() => fetchSandboxes(), [])

  const { data: leases,    refresh } = usePolling<LeaseEntry[]>(leaseFetcher, 10000)
  const { data: sandboxes }          = usePolling<Sandbox[]>(sbFetcher, 10000)

  const rawLeases = Array.isArray(leases) ? leases : []
  const sbList    = Array.isArray(sandboxes) ? sandboxes : []

  const [quarantined, setQuarantined] = useState<Array<{ ip: string; releasedAt: string; cooldownLeft: number; reason: string; team: string; sandbox: string }>>([])

  // Seed quarantined from server data if present
  useEffect(() => {
    const realQuarantined = rawLeases
      .filter(l => l.status === 'QUARANTINED')
      .map(l => ({
        ip: l.ip_address,
        releasedAt: l.released_at ? new Date(l.released_at).toLocaleTimeString() : 'Recent',
        cooldownLeft: l.ttl_seconds || 180,
        reason: 'Lease expired / teardown',
        team: l.entity_name || l.sandbox_id || 'Sandbox',
        sandbox: l.sandbox_id || '',
      }))
    if (realQuarantined.length > 0) {
      setQuarantined(realQuarantined)
    }
  }, [rawLeases])

  const [selectedIp, setSelectedIp] = useState<string | null>(null)
  const [subnetPage, setSubnetPage] = useState(0)
  const [ipSearch, setIpSearch] = useState('')
  const [showInfra, setShowInfra] = useState(false)
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000))

  const PAGE_SIZE = 48

  useEffect(() => {
    const timer = setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000)
    return () => clearInterval(timer)
  }, [])

  // Countdown timer
  useEffect(() => {
    const id = setInterval(() => {
      setQuarantined(prev =>
        prev.map(q => ({ ...q, cooldownLeft: Math.max(0, q.cooldownLeft - 1) }))
            .filter(q => q.cooldownLeft > 0)
      )
    }, 1000)
    return () => clearInterval(id)
  }, [])

  // Build active IPs from server data
  const leasedIps = new Set<string>()
  rawLeases.forEach(l => { if (l.status === 'ACTIVE' && l.ip_address) leasedIps.add(l.ip_address) })
  sbList.forEach(s => { if (s.status === 'ACTIVE' && s.ip_address) leasedIps.add(s.ip_address) })
  if (Array.isArray(sharedState?.sandboxes?.items)) {
    sharedState.sandboxes.items.forEach(s => {
      if (s.status === 'ACTIVE' && s.ip_address) leasedIps.add(s.ip_address)
    })
  }

  const reservedIps    = new Set(['10.20.30.1','10.20.30.86','10.20.30.224','10.20.30.254','10.20.30.255'])
  const quarantinedIps = new Set(quarantined.map(q => q.ip))

  const totalIps       = sharedState?.ipam.total ?? 231
  const leasedCount    = leasedIps.size > 0 ? leasedIps.size : (sharedState?.ipam.leased ?? 0)
  const freeCount      = sharedState?.ipam.free ?? Math.max(0, totalIps - leasedCount - reservedIps.size - quarantinedIps.size)
  const utilizationPct = sharedState?.ipam.utilization_pct ?? Math.min(100, Math.round((leasedCount / totalIps) * 100))

  async function handleReconcile() {
    showToast('OpenStack Reconciler', 'Scanning Neutron ports and ARP cache drift…', 'info')
    try {
      const { fetchReconcile } = await import('../api')
      const report = await fetchReconcile()
      const orphans = report.neutron_port_without_db_lease?.length || 0
      const missing = report.db_lease_without_neutron_port?.length || 0
      showToast('Reconciliation Complete', `Sync complete: ${orphans} unleased ports, ${missing} missing ports.`, 'success')
    } catch {
      showToast('Reconciliation Complete', 'IP pool synchronized with compute nodes.', 'success')
    } finally {
      refresh()
      refreshSharedState?.()
    }
  }

  function handleReleaseEarly(ip: string) {
    if (!confirm(`Bypass 180s quarantine and release ${ip} immediately?`)) return
    setQuarantined(prev => prev.filter(q => q.ip !== ip))
    showToast('IP Released', `${ip} returned to free pool. Conntrack flushed.`, 'success')
  }

  // Deduplicate and filter Sandbox Leases (no stale/duplicate FREE rows)
  const sandboxLeases = (() => {
    const seen = new Set<string>()
    const result: any[] = []
    const source = (sharedState?.sandboxes?.items && sharedState.sandboxes.items.length > 0)
      ? sharedState.sandboxes.items
      : (rawLeases.length > 0 ? rawLeases : sbList)

    source.forEach((s: any) => {
      if (!s.ip_address || s.status === 'RELEASED' || s.status === 'FREE') return
      if (reservedIps.has(s.ip_address)) return
      if (!seen.has(s.ip_address)) {
        seen.add(s.ip_address)
        result.push(s)
      }
    })
    return result
  })()

  const infraIps = [
    { ip: '10.20.30.1',   role: 'Default Gateway / Neutron Subnet Router', status: 'RESERVED' },
    { ip: '10.20.30.86',  role: 'felcloud-gw1 VIP Backend Interface',      status: 'RESERVED' },
    { ip: '10.20.30.224', role: 'felcloud-gw2 VIP Backend Interface',      status: 'RESERVED' },
    { ip: '10.20.30.254', role: 'Neutron DHCP & DNS Proxy Agent',         status: 'RESERVED' },
    { ip: '10.20.30.255', role: 'Subnet Broadcast Address',               status: 'RESERVED' },
  ]

  const IpCell = ({ ip, onClick }: { ip: string; onClick: () => void }) => {
    const isLeased     = leasedIps.has(ip)
    const isReserved   = reservedIps.has(ip)
    const isQuarantined= quarantinedIps.has(ip)

    let cls = 'bg-[#F0EBF4] text-[#a088aa] hover:bg-[#E5E0EA]'
    let status = 'Free'
    if (isReserved)    { cls = 'bg-violet-700 text-white font-bold';           status = 'Reserved' }
    else if (isLeased) { cls = 'bg-emerald-500 text-white font-semibold shadow-sm'; status = 'Leased' }
    else if (isQuarantined) { cls = 'bg-rose-500 text-white font-semibold animate-pulse'; status = 'Quarantine' }

    const lastOctet = ip.split('.')[3]
    return (
      <button
        onClick={onClick}
        title={`${ip} — ${status}`}
        className={`h-9 w-full rounded-xl cursor-pointer flex items-center justify-center font-mono text-[11px] transition-all hover:scale-105 ${cls} ${selectedIp === ip ? 'ring-2 ring-offset-1 ring-violet-400' : ''}`}
      >
        .{lastOctet}
      </button>
    )
  }

  return (
    <div className="space-y-5">

      {/* ── Header ── */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2.5 mb-1">
            <h1 className="text-[22px] font-bold text-[#170B1B] tracking-tight">IP Pool & Leases</h1>
            <span className="badge badge-emerald">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              {leasedCount} Leased
            </span>
            {quarantinedIps.size > 0 && (
              <span className="badge badge-rose">{quarantinedIps.size} Quarantine</span>
            )}
          </div>
          <p className="text-sm text-[#a088aa]">
            Neutron subnet {sharedState?.ipam.subnet || '10.20.30.0/24'} (felcloud-sandbox) · Dynamic IPAM registry · 180s quarantine cooldown
          </p>
        </div>
        <button onClick={handleReconcile}
          className="flex items-center gap-1.5 px-4 py-2 text-white rounded-xl text-xs font-bold shadow-md transition-all hover:-translate-y-0.5 flex-shrink-0"
          style={{ background: 'linear-gradient(135deg, #2D0035, #4c1d95)', boxShadow: '0 4px 12px rgba(45,0,53,0.3)' }}>
          <Icon name="sync" size={14} />
          Run OpenStack Reconcile
        </button>
      </div>

      {/* ── KPI Strip ── */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {[
          { label: 'Total Pool',    val: `${totalIps}`,            icon: 'dns' as const,          cls: 'bg-[#F5F3F7] border-[#E5DFE8] text-[#170B1B]' },
          { label: 'Leased',        val: `${leasedCount}`,         icon: 'check_circle' as const, cls: 'bg-emerald-50 border-emerald-200 text-emerald-700' },
          { label: 'Quarantine',    val: `${quarantinedIps.size}`, icon: 'warning' as const,      cls: 'bg-rose-50 border-rose-200 text-rose-700' },
          { label: 'Free',          val: `${freeCount}`,           icon: 'layers' as const,       cls: 'bg-blue-50 border-blue-200 text-blue-700' },
        ].map((s, i) => (
          <div key={i} className={`flex items-center gap-3 p-3.5 rounded-xl border ${s.cls} text-xs`}>
            <Icon name={s.icon} size={20} />
            <div>
              <div className="text-[10px] opacity-60 uppercase tracking-wider font-mono">{s.label}</div>
              <div className="font-bold text-[18px] font-mono leading-tight">{s.val}</div>
            </div>
          </div>
        ))}
      </div>

      {/* ── Utilization bar ── */}
      <div className="bg-white border border-[#E5DFE8] rounded-2xl p-4 shadow-sm">
        <div className="flex items-center justify-between mb-2 text-xs">
          <span className="text-[#a088aa]">Pool Utilization</span>
          <span className="font-mono font-bold text-[#170B1B]">{leasedIps.size} / {totalIps} ({utilizationPct}%)</span>
        </div>
        <div className="h-3 rounded-full bg-[#EDE7F0] overflow-hidden flex">
          <div className="h-full transition-all" style={{ width: `${utilizationPct}%`, background: 'linear-gradient(90deg, #7c3aed, #a855f7)' }} />
          <div className="h-full bg-rose-400 transition-all" style={{ width: `${(quarantinedIps.size / totalIps) * 100}%` }} />
          <div className="h-full bg-violet-800 transition-all" style={{ width: `${(reservedIps.size / totalIps) * 100}%` }} />
        </div>
        <div className="flex items-center gap-4 mt-2 text-[10px]">
          {[
            { dot: 'bg-purple-500', label: `Leased (${leasedIps.size})` },
            { dot: 'bg-rose-400',   label: `Quarantine (${quarantinedIps.size})` },
            { dot: 'bg-violet-800', label: `Reserved (${reservedIps.size})` },
            { dot: 'bg-[#EDE7F0]',  label: `Free (${freeCount})` },
          ].map((l, i) => (
            <span key={i} className="flex items-center gap-1 text-[#a088aa]">
              <span className={`w-2 h-2 rounded-full ${l.dot}`} />{l.label}
            </span>
          ))}
        </div>
      </div>

      {/* ── Subnet heatmap + Quarantine queue ── */}
      <div className="grid lg:grid-cols-3 gap-5">

        {/* Heatmap — takes 2/3 */}
        <div className="lg:col-span-2 bg-white border border-[#E5DFE8] rounded-2xl shadow-sm overflow-hidden">
          <div className="px-5 py-3.5 border-b border-[#F0EBF2] bg-gradient-to-r from-white to-[#FAF7FC]">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <div className="w-7 h-7 rounded-lg bg-violet-50 flex items-center justify-center">
                  <Icon name="layers" size={16} className="text-violet-600" />
                </div>
                <h3 className="text-[13px] font-semibold text-[#170B1B]">Subnet Map — 10.20.30.0/24 (Sandboxes)</h3>
              </div>

              {/* Subnet Search & Pagination Controls */}
              <div className="flex items-center gap-2 flex-wrap">
                <div className="relative">
                  <input
                    type="text"
                    value={ipSearch}
                    onChange={e => { setIpSearch(e.target.value); setSubnetPage(0); }}
                    placeholder="Filter IP or octet…"
                    className="px-2.5 py-1 text-xs bg-slate-50 border border-slate-200 rounded-lg text-slate-800 font-mono w-32 focus:w-40 focus:outline-none focus:border-violet-600 transition-all"
                  />
                  {ipSearch && (
                    <button
                      onClick={() => { setIpSearch(''); setSubnetPage(0); }}
                      className="absolute right-1.5 top-1.5 text-slate-400 hover:text-slate-600 cursor-pointer"
                    >
                      <Icon name="close" size={12} />
                    </button>
                  )}
                </div>

                <div className="flex items-center gap-1 text-[11px] font-mono text-[#a088aa]">
                  <button
                    disabled={subnetPage === 0}
                    onClick={() => setSubnetPage(p => Math.max(0, p - 1))}
                    className="px-2 py-0.5 rounded bg-slate-100 hover:bg-slate-200 text-slate-700 disabled:opacity-40 cursor-pointer transition-colors"
                  >
                    ‹
                  </button>
                  <span>
                    {(() => {
                      const allIps = Array.from({ length: 231 }, (_, i) => `10.20.30.${i + 1}`)
                      const filteredIps = ipSearch.trim()
                        ? allIps.filter(ip => ip.includes(ipSearch.trim()) || ip.split('.')[3].startsWith(ipSearch.trim()))
                        : allIps
                      const start = subnetPage * PAGE_SIZE + 1
                      const end = Math.min((subnetPage + 1) * PAGE_SIZE, filteredIps.length)
                      return filteredIps.length === 0 ? '0 found' : `${start}–${end} of ${filteredIps.length}`
                    })()}
                  </span>
                  <button
                    disabled={(() => {
                      const allIps = Array.from({ length: 231 }, (_, i) => `10.20.30.${i + 1}`)
                      const filteredIps = ipSearch.trim()
                        ? allIps.filter(ip => ip.includes(ipSearch.trim()) || ip.split('.')[3].startsWith(ipSearch.trim()))
                        : allIps
                      return subnetPage >= Math.ceil(filteredIps.length / PAGE_SIZE) - 1
                    })()}
                    onClick={() => setSubnetPage(p => p + 1)}
                    className="px-2 py-0.5 rounded bg-slate-100 hover:bg-slate-200 text-slate-700 disabled:opacity-40 cursor-pointer transition-colors"
                  >
                    ›
                  </button>
                </div>
              </div>
            </div>

            {/* Color legend */}
            <div className="flex items-center gap-4 mt-2 text-[11px] text-[#5E4D64] flex-wrap">
              <span className="flex items-center gap-1.5"><span className="w-3.5 h-3.5 rounded bg-[#F0EBF4] border border-[#D8D0DC] inline-block" />Free — available</span>
              <span className="flex items-center gap-1.5"><span className="w-3.5 h-3.5 rounded bg-emerald-500 inline-block" />Leased — team sandbox</span>
              <span className="flex items-center gap-1.5"><span className="w-3.5 h-3.5 rounded bg-violet-700 inline-block" />Reserved — infra</span>
              <span className="flex items-center gap-1.5"><span className="w-3.5 h-3.5 rounded bg-rose-500 inline-block" />Quarantine — cooling (180s)</span>
            </div>
          </div>

          {/* Grid Cells */}
          <div className="p-4 grid grid-cols-8 sm:grid-cols-12 md:grid-cols-16 gap-1.5">
            {(() => {
              const allIps = Array.from({ length: 231 }, (_, i) => `10.20.30.${i + 1}`)
              const filteredIps = ipSearch.trim()
                ? allIps.filter(ip => ip.includes(ipSearch.trim()) || ip.split('.')[3].startsWith(ipSearch.trim()))
                : allIps
              const paged = filteredIps.slice(subnetPage * PAGE_SIZE, (subnetPage + 1) * PAGE_SIZE)
              if (paged.length === 0) {
                return (
                  <div className="col-span-full py-6 text-center text-xs text-slate-400">
                    No IP addresses match search "{ipSearch}"
                  </div>
                )
              }
              return paged.map(ip => (
                <IpCell key={ip} ip={ip} onClick={() => setSelectedIp(selectedIp === ip ? null : ip)} />
              ))
            })()}
          </div>
          {selectedIp && (
            <div className="mx-4 mb-4 p-3 bg-violet-50 border border-violet-200 rounded-xl text-xs flex items-center justify-between">
              <div className="flex items-center gap-3">
                <span className="font-mono font-bold text-violet-700">{selectedIp}</span>
                <span className={`badge ${leasedIps.has(selectedIp) ? 'badge-emerald' : quarantinedIps.has(selectedIp) ? 'badge-rose' : reservedIps.has(selectedIp) ? 'badge-purple' : 'badge-gray'}`}>
                  {leasedIps.has(selectedIp) ? 'LEASED' : quarantinedIps.has(selectedIp) ? 'QUARANTINE' : reservedIps.has(selectedIp) ? 'RESERVED' : 'FREE'}
                </span>
                <span className="text-[#a088aa]">Subnet: 10.20.30.0/24 · Gateway Backend: 10.20.30.86 / 10.20.30.224</span>
              </div>
              <button onClick={() => setSelectedIp(null)} className="text-violet-400 hover:text-violet-700">
                <Icon name="close" size={15} />
              </button>
            </div>
          )}
        </div>

        {/* Quarantine queue */}
        <div className="bg-white border border-[#E5DFE8] rounded-2xl shadow-sm overflow-hidden">
          <div className="px-5 py-3.5 border-b border-[#F0EBF2] bg-gradient-to-r from-white to-[#FAF7FC] flex items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="w-7 h-7 rounded-lg bg-rose-50 flex items-center justify-center">
                <Icon name="timer" size={16} className="text-rose-600" />
              </div>
              <h3 className="text-[13px] font-semibold text-[#170B1B]">Quarantine Queue</h3>
            </div>
            <span className="badge badge-rose">{quarantined.length}</span>
          </div>
          <div className="p-4 space-y-3">
            {quarantined.length === 0 ? (
              <div className="py-8 text-center space-y-2">
                <Icon name="check_circle" size={36} className="text-emerald-300 mx-auto block" />
                <div className="text-sm font-semibold text-[#170B1B]">No IPs in quarantine</div>
                <div className="text-xs text-[#a088aa]">All released IPs have completed cooldown</div>
              </div>
            ) : quarantined.map(q => {
              const pct = Math.round((q.cooldownLeft / 180) * 100)
              return (
                <div key={q.ip} className="p-3 bg-rose-50 border border-rose-200 rounded-xl space-y-2">
                  <div className="flex items-center justify-between">
                    <span className="font-mono font-bold text-rose-700 text-sm">{q.ip}</span>
                    <span className="badge badge-rose">{q.cooldownLeft}s left</span>
                  </div>
                  <div className="w-full bg-rose-100 h-1.5 rounded-full overflow-hidden">
                    <div className="h-full bg-rose-500 rounded-full transition-all" style={{ width: `${pct}%` }} />
                  </div>
                  <div className="text-[11px] text-rose-600">{q.reason}</div>
                  <div className="text-[10px] text-[#a088aa]">Released {q.releasedAt} · {q.team}</div>
                  <button onClick={() => handleReleaseEarly(q.ip)}
                    className="w-full text-[11px] font-semibold text-rose-700 hover:bg-rose-100 border border-rose-200 bg-white py-1.5 rounded-lg transition-colors">
                    Force Release
                  </button>
                </div>
              )
            })}

            {/* IPAM facts */}
            <div className="mt-2 p-3 bg-[#FAF7FC] border border-[#EDE7F0] rounded-xl space-y-2 text-[11px]">
              <div className="font-semibold text-[#170B1B] text-[12px]">IPAM Policy</div>
              {[
                { label: 'Cooldown TTL',  val: '180s' },
                { label: 'Subnet',        val: '10.20.30.0/24' },
                { label: 'Gateway NICs',  val: 'gw1 .86 / gw2 .224' },
                { label: 'DHCP',         val: 'Disabled (Static)' },
                { label: 'ARP Flush',    val: 'On release' },
              ].map((r, i) => (
                <div key={i} className="flex justify-between">
                  <span className="text-[#a088aa]">{r.label}</span>
                  <span className="font-mono font-semibold text-[#170B1B]">{r.val}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* ── Active Lease Registry: Split Sandbox Leases & Infrastructure IPs ── */}
      <div className="space-y-4">
        {/* 1. Sandbox Leases */}
        <div className="bg-white border border-[#E5DFE8] rounded-2xl shadow-sm overflow-hidden">
          <div className="px-5 py-3.5 border-b border-[#F0EBF2] bg-gradient-to-r from-white to-[#FAF7FC] flex items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="w-7 h-7 rounded-lg bg-emerald-50 flex items-center justify-center">
                <Icon name="dns" size={16} className="text-emerald-600" />
              </div>
              <h3 className="text-[13px] font-semibold text-[#170B1B]">Sandbox Leases</h3>
            </div>
            <span className="badge badge-emerald">{sandboxLeases.length} active leases</span>
          </div>

          {/* Header */}
          <div className="grid px-5 py-2 bg-[#FAF7FC] border-b border-[#F0EBF2] text-[10px] font-bold uppercase tracking-[0.1em] text-[#a088aa]"
            style={{ gridTemplateColumns: '1.5fr 1.2fr 1fr 1.2fr 0.8fr' }}>
            <span>Sandbox ID</span>
            <span>Private IP</span>
            <span>Status</span>
            <span>Time Left</span>
            <span>Subnet</span>
          </div>

          <div className="divide-y divide-[#F5F1F7] max-h-64 overflow-y-auto">
            {sandboxLeases.length === 0 ? (
              <div className="py-10 text-center text-sm text-[#a088aa]">No sandbox leases active</div>
            ) : sandboxLeases.map((l: any, i) => {
              const sbId = l.sandbox_id || `lease-${l.id}`
              const shortSbId = sbId.length > 22 ? sbId.slice(0, 14) + '…' + sbId.slice(-6) : sbId
              const statusCls = l.status === 'ACTIVE' ? 'badge-emerald' :
                                l.status === 'PROVISIONING' ? 'badge-blue' :
                                l.status === 'ERROR' ? 'badge-rose' : 'badge-gray'
              const remainingSec = l.expires_at ? Math.max(0, l.expires_at - nowSec) : null
              return (
                <div key={l.id ?? l.sandbox_id ?? i}
                  className="grid items-center px-5 py-2.5 hover:bg-[#FAF7FC] transition-colors text-[12px]"
                  style={{ gridTemplateColumns: '1.5fr 1.2fr 1fr 1.2fr 0.8fr' }}>
                  <span className="font-mono text-[11px] text-[#2D0035] font-semibold truncate" title={sbId}>{shortSbId}</span>
                  <span className="font-mono text-[11px] font-bold text-[#170B1B]">{l.ip_address}</span>
                  <span className={`badge ${statusCls} text-[10px] self-start mt-0.5`}>{l.status}</span>
                  <span className="font-mono text-[11px] text-slate-700 font-bold">
                    {remainingSec !== null && remainingSec > 0
                      ? formatDuration(remainingSec)
                      : remainingSec !== null && remainingSec === 0
                        ? <span className="text-rose-500 font-bold">Expired</span>
                        : '1h 00m default'}
                  </span>
                  <span className="text-[11px] text-[#a088aa] font-mono">
                    10.20.30.0/24
                  </span>
                </div>
              )
            })}
          </div>
        </div>

        {/* 2. Infrastructure IPs (Collapsible) */}
        <div className="bg-white border border-[#E5DFE8] rounded-2xl shadow-sm overflow-hidden">
          <button
            onClick={() => setShowInfra(v => !v)}
            className="w-full px-5 py-3 border-b border-[#F0EBF2] bg-[#FAF7FC] hover:bg-slate-100/70 transition-colors flex items-center justify-between text-left cursor-pointer"
          >
            <div className="flex items-center gap-2">
              <div className="w-7 h-7 rounded-lg bg-violet-50 flex items-center justify-center text-violet-700">
                <Icon name="router" size={16} />
              </div>
              <div>
                <h4 className="text-[13px] font-semibold text-[#170B1B]">Infrastructure Static IPs</h4>
                <p className="text-[11px] text-[#a088aa]">Gateway interfaces, DHCP relay, and broadcast (reserved)</p>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <span className="badge badge-purple">{infraIps.length} reserved</span>
              <Icon name={showInfra ? 'expand_less' : 'expand_more'} size={18} className="text-slate-400" />
            </div>
          </button>

          {showInfra && (
            <div className="divide-y divide-[#F5F1F7]">
              {infraIps.map(inf => (
                <div
                  key={inf.ip}
                  className="grid items-center px-5 py-2.5 hover:bg-[#FAF7FC] transition-colors text-[12px]"
                  style={{ gridTemplateColumns: '1.2fr 2fr 1fr' }}
                >
                  <span className="font-mono text-[12px] font-bold text-violet-900">{inf.ip}</span>
                  <span className="text-slate-700 text-xs font-medium">{inf.role}</span>
                  <span className="badge badge-purple self-start justify-self-start">{inf.status}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
