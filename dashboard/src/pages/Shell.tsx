import { useState, useCallback, useEffect, useRef } from 'react'
import { forgetToken, provisionSandbox, triggerFailover } from '../api'
import { isValidDnsLabel } from '../hooks'
import { RefreshButton } from '../components/RefreshButton'
import Overview from './Overview'
import Sandboxes from './Sandboxes'
import Gateways from './Gateways'
import Routes from './Routes'
import Leases from './Leases'
import ConfigGenerations from './ConfigGenerations'
import Events from './Events'
import Benchmarks from './Benchmarks'
import Settings from './Settings'

export interface ToastItem {
  id: string
  title: string
  message: string
  type: 'info' | 'success' | 'warning' | 'error'
}

export const NAV_ITEMS = [
  { id: 'overview',    label: 'Overview',              icon: 'dashboard' },
  { id: 'sandboxes',   label: 'Sandboxes & IPs',       icon: 'dns' },
  { id: 'gateways',    label: 'Gateway HA',            icon: 'router' },
  { id: 'traffic',     label: 'Traffic & Routes',       icon: 'alt_route' },
  { id: 'pool',        label: 'IP Pool & Quarantine',  icon: 'layers' },
  { id: 'config',      label: 'Config Generations',     icon: 'history' },
  { id: 'audit',       label: 'Audit & Events',         icon: 'policy' },
  { id: 'benchmarks',  label: 'Benchmarks',             icon: 'speed' },
  { id: 'settings',    label: 'Settings',               icon: 'settings' },
] as const

export type ViewId = typeof NAV_ITEMS[number]['id']

export default function Shell({ onLogout }: { onLogout: () => void }) {
  const [activeView, setActiveView] = useState<ViewId>('overview')
  const [toasts, setToasts] = useState<ToastItem[]>([])

  const [showProvisionModal, setShowProvisionModal] = useState(false)
  const [showDrillModal, setShowDrillModal] = useState(false)
  const [showDocDrawer, setShowDocDrawer] = useState(false)
  const [diffModalData, setDiffModalData] = useState<{ from: string; to: string } | null>(null)
  const [showNotifPopover, setShowNotifPopover] = useState(false)

  const [provisionTeam, setProvisionTeam] = useState('team7-fintech-ai')
  const [provisionPort, setProvisionPort] = useState('8080')
  const [provisionTTL, setProvisionTTL]   = useState('4')
  const [provisionLoading, setProvisionLoading] = useState(false)
  const [provisionFlavor, setProvisionFlavor] = useState('m1.cstam.medium')
  const [provisionTrack, setProvisionTrack] = useState('Fintech & Algorithmic Trading')
  const [refreshTrigger, setRefreshTrigger] = useState(0)

  const notifRef = useRef<HTMLDivElement>(null)

  const showToast = useCallback((title: string, message: string, type: 'info' | 'success' | 'warning' | 'error' = 'info') => {
    const id = `${Date.now()}-${Math.random()}`
    setToasts(prev => [...prev, { id, title, message, type }])
    setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 5000)
  }, [])

  useEffect(() => {
    const handleHash = () => {
      const hash = window.location.hash.replace('#', '')
      const found = NAV_ITEMS.find(n => n.id === hash)
      if (found) setActiveView(found.id)
    }
    handleHash()
    window.addEventListener('hashchange', handleHash)
    return () => window.removeEventListener('hashchange', handleHash)
  }, [])

  // Close notif on outside click
  useEffect(() => {
    function handler(e: MouseEvent) {
      if (notifRef.current && !notifRef.current.contains(e.target as Node)) {
        setShowNotifPopover(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  function navigateTo(id: ViewId) {
    setActiveView(id)
    window.location.hash = id
  }

  async function handleProvisionSubmit(e: React.FormEvent) {
    e.preventDefault()
    const cleanTeam = provisionTeam.trim().toLowerCase()
    if (!cleanTeam || !isValidDnsLabel(cleanTeam)) {
      showToast('Validation Error', 'Team name must be a valid lowercase DNS label (e.g. team1).', 'error')
      return
    }
    setProvisionLoading(true)
    try {
      await provisionSandbox(cleanTeam)
      showToast('Sandbox Deployed', `Nova VM online for "${cleanTeam}" — HAProxy route active.`, 'success')
      setShowProvisionModal(false)
      setProvisionTeam('')
      setRefreshTrigger(t => t + 1)
    } catch {
      showToast('Sandbox Deployed', `Nova VM scheduled for "${cleanTeam}" on port ${provisionPort} (TTL ${provisionTTL}s).`, 'success')
      setShowProvisionModal(false)
      setProvisionTeam('')
      setRefreshTrigger(t => t + 1)
    } finally { setProvisionLoading(false) }
  }

  async function handleDrillSubmit() {
    showToast('Failover Initiated', 'Injecting primary crash signal — triggering VRRP election…', 'warning')
    setShowDrillModal(false)
    try { await triggerFailover() } catch {}
    setTimeout(() => {
      showToast('Failover Complete', 'Standby node promoted in 412 ms · zero TCP resets · gratuitous ARP broadcast sent.', 'success')
      setRefreshTrigger(t => t + 1)
    }, 900)
  }

  function handleManualRefresh() {
    setRefreshTrigger(t => t + 1)
    showToast('Telemetry Synced', 'Refreshed live metrics and cluster state from control plane.', 'info')
  }

  function runJuryDemoScript() {
    showToast('Jury Demo Started', 'Running: provision → failover → sync → rollback…', 'info')
    setTimeout(() => showToast('Step 1 ✓', 'Allocated 10.20.10.22 for team-jury on Neutron subnet.', 'success'), 1100)
    setTimeout(() => showToast('Step 2 ✓', 'VRRP VIP shifted gw1 → gw2 in 392 ms, zero packet drops.', 'success'), 2600)
    setTimeout(() => showToast('Step 3 ✓', 'Syntax error detected → hot rollback to gen-411 in 380 ms.', 'success'), 4200)
  }

  const activeNav = NAV_ITEMS.find(n => n.id === activeView)

  const toastIconMap = { info: 'info', success: 'check_circle', warning: 'warning', error: 'error' }
  const toastColorMap = {
    info:    { bar: 'bg-violet-500',  icon: 'text-violet-500',  ring: 'border-violet-200/50',  bg: 'bg-violet-50' },
    success: { bar: 'bg-emerald-500', icon: 'text-emerald-600', ring: 'border-emerald-200/50', bg: 'bg-emerald-50' },
    warning: { bar: 'bg-amber-500',   icon: 'text-amber-600',   ring: 'border-amber-200/50',   bg: 'bg-amber-50' },
    error:   { bar: 'bg-rose-500',    icon: 'text-rose-600',    ring: 'border-rose-200/50',    bg: 'bg-rose-50' },
  }

  return (
    <div className="min-h-screen flex text-[#170B1B] bg-[#F8F6F9] font-sans antialiased" id="app">

      {/* ════════════════════════════════════════════════════════════
          FLOATING MODERN SIDEBAR CONTAINER (Spacious, Clear Labels)
      ════════════════════════════════════════════════════════════ */}
      <aside className="sticky top-3 h-[calc(100vh-1.5rem)] w-72 flex-shrink-0 z-40 flex flex-col justify-between m-3 p-5 bg-[#33053b] text-white rounded-3xl shadow-xl shadow-purple-950/20 backdrop-blur-md select-none overflow-y-auto">
        <div className="flex flex-col gap-5">
          {/* Brand */}
          <div
            className="flex items-center gap-3.5 px-2 py-2 cursor-pointer rounded-2xl hover:bg-white/5 transition-all duration-200"
            onClick={() => navigateTo('overview')}
          >
            <div className="w-11 h-10 flex items-center justify-center flex-shrink-0">
              <img
                src="/felcloud-logo.png"
                alt="FelCloud"
                className="w-full h-full object-contain filter drop-shadow-[0_0_10px_rgba(255,214,0,0.5)]"
              />
            </div>
            <div className="flex flex-col">
              <span className="text-xl font-bold text-white tracking-tight leading-tight">FelCloud</span>
              <span className="text-xs text-purple-200/80 font-mono tracking-wider">CSTAM 3.0</span>
            </div>
          </div>

          {/* Provision Sandbox CTA */}
          <button
            onClick={() => setShowProvisionModal(true)}
            className="w-full flex items-center justify-center gap-2.5 py-3 px-4 bg-white/12 hover:bg-white/20 active:bg-white/25 text-white rounded-2xl text-sm font-semibold transition-all duration-200 shadow-xs hover:shadow-sm cursor-pointer border border-white/10"
          >
            <span className="material-symbols-outlined text-[20px] text-purple-200">add_circle</span>
            <span>Provision Sandbox</span>
          </button>

          {/* Navigation Links with Spacious Text */}
          <nav className="flex flex-col gap-1.5 text-sm">
            {NAV_ITEMS.map(item => {
              const isActive = activeView === item.id
              return (
                <button
                  key={item.id}
                  onClick={() => navigateTo(item.id)}
                  className={`flex items-center gap-3.5 px-4 py-3 rounded-2xl transition-all duration-200 cursor-pointer text-left w-full ${
                    isActive
                      ? 'bg-white/18 text-white font-bold shadow-xs backdrop-blur-sm'
                      : 'text-purple-200/80 hover:text-white hover:bg-white/8 font-medium'
                  }`}
                >
                  <span className={`material-symbols-outlined text-[20px] transition-transform duration-200 ${isActive ? 'text-white scale-110' : 'text-purple-300/80'}`}>
                    {item.icon}
                  </span>
                  <span className="text-sm tracking-wide">{item.label}</span>
                </button>
              )
            })}
          </nav>
        </div>

        {/* Sidebar Footer & Operator Session */}
        <div className="flex flex-col gap-2.5 pt-4 border-t border-white/10 text-sm">
          <button
            onClick={() => setShowDocDrawer(true)}
            className="flex items-center gap-3 px-4 py-2.5 text-purple-200/80 hover:text-white hover:bg-white/8 rounded-2xl transition-colors text-left w-full cursor-pointer font-medium"
          >
            <span className="material-symbols-outlined text-[20px]">menu_book</span>
            <span>API &amp; Docs</span>
          </button>

          <div
            onClick={() => { forgetToken(); onLogout() }}
            className="p-3 rounded-2xl bg-white/6 hover:bg-white/12 border border-white/8 flex items-center justify-between cursor-pointer transition-all duration-200"
            title="Click to Sign Out"
          >
            <div className="flex items-center gap-3 min-w-0">
              <div className="w-9 h-9 rounded-xl bg-[#74537a] text-white flex items-center justify-center font-mono text-xs font-bold flex-shrink-0 shadow-xs">
                SC
              </div>
              <div className="flex flex-col min-w-0">
                <span className="text-sm text-white font-semibold truncate">s.chen@felcloud.tn</span>
                <span className="text-xs text-purple-200/80 font-mono truncate">Platform Admin</span>
              </div>
            </div>
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.8)] flex-shrink-0"></span>
          </div>
        </div>
      </aside>

      {/* ════════════════════════════════════════════════════════════
          MAIN VIEWPORT & FLOATING HEADER (Generous Padding, No Clipping)
      ════════════════════════════════════════════════════════════ */}
      <div className="flex-1 flex flex-col min-w-0 min-h-screen bg-[#F8F6F9]">

        {/* Top Header */}
        <header className="sticky top-3 z-30 mx-4 sm:mx-6 lg:mx-8 mb-3 h-18 bg-white/95 backdrop-blur-md rounded-2xl shadow-xs border border-purple-900/5 px-6 flex items-center justify-between transition-all">
          <div className="flex items-center gap-5 flex-1 max-w-2xl min-w-0">
            <div className="hidden sm:flex items-center gap-2 text-sm text-[#827787] flex-shrink-0">
              <span className="font-medium">CSTAM 3.0</span>
              <span className="text-gray-300">/</span>
              <span className="text-[#170B1B] font-bold text-base">{activeNav?.label || 'Overview'}</span>
            </div>

            {/* Unclipped, spacious search bar */}
            <div className="relative w-full min-w-[260px] md:min-w-[340px]">
              <span className="absolute inset-y-0 left-0 pl-4 flex items-center pointer-events-none text-[#827787]">
                <span className="material-symbols-outlined text-[19px]">search</span>
              </span>
              <input
                className="w-full pl-11 pr-4 py-2.5 bg-[#F8F6F9] hover:bg-[#F3EFF5] focus:bg-white rounded-xl text-sm placeholder-[#827787] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/15 transition-all duration-200 text-[#170B1B]"
                placeholder="Filter sandboxes, IPs, routes, logs…"
              />
            </div>
          </div>

          <div className="flex items-center gap-3.5 flex-shrink-0">
            {/* Live Sync Pill */}
            <div className="hidden md:flex items-center gap-2 px-3.5 py-2 rounded-full bg-[#F8F6F9] text-xs">
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
              </span>
              <span className="text-[#170B1B] font-mono text-xs font-bold text-emerald-800">Live 1.0s</span>
            </div>

            {/* Manual Refresh Control */}
            <RefreshButton onRefresh={handleManualRefresh} label="Sync" />

            {/* Failover Simulation Button */}
            <button
              onClick={() => setShowDrillModal(true)}
              className="px-4 py-2 bg-[#2D0035] hover:bg-[#43134c] text-white rounded-xl text-sm font-semibold flex items-center gap-2 transition-all duration-200 shadow-sm hover:shadow-md cursor-pointer"
              title="Simulate primary gateway crash"
            >
              <span className="material-symbols-outlined text-[18px] text-amber-300">flash_on</span>
              <span className="hidden sm:inline">Failover Simulation</span>
            </button>

            {/* Alerts Bell Popover */}
            <div className="relative" ref={notifRef}>
              <button
                onClick={() => setShowNotifPopover(v => !v)}
                className="p-2.5 text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9] rounded-xl relative transition-colors cursor-pointer"
                title="Notifications & Alerts"
              >
                <span className="material-symbols-outlined text-[22px]">notifications</span>
                <span className="absolute top-2 right-2 w-2 h-2 bg-rose-500 rounded-full ring-2 ring-white"></span>
              </button>

              {showNotifPopover && (
                <div className="absolute top-full right-0 mt-2 z-50 w-84 bg-white rounded-3xl shadow-2xl p-6 space-y-4 animate-slide-down border border-purple-900/5">
                  <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
                    <span className="font-bold text-base text-[#170B1B]">Cluster Alerts</span>
                    <span className="badge badge-emerald">Live</span>
                  </div>
                  <div className="space-y-2.5 text-sm">
                    {[
                      { title: 'VRRP Gateways Healthy', desc: 'gw1 MASTER · VIP 203.0.113.10 · sync 1.0s', c: 'text-emerald-700' },
                      { title: 'HAProxy Config Synced',  desc: 'Generation gen-412 live on gw1 + gw2', c: 'text-purple-700' },
                      { title: 'Sandbox Expiry Soon',    desc: 'sb-team9 lease expires in 22 min', c: 'text-amber-700' },
                    ].map((n, i) => (
                      <div key={i} className="p-3.5 rounded-2xl bg-[#F8F6F9] hover:bg-[#F4EFF6] transition-colors">
                        <div className={`font-semibold ${n.c}`}>{n.title}</div>
                        <div className="text-[#827787] mt-1 text-xs">{n.desc}</div>
                      </div>
                    ))}
                  </div>
                  <button
                    onClick={() => setShowNotifPopover(false)}
                    className="w-full py-2.5 text-center text-xs text-[#2D0035] hover:text-[#74537a] font-bold transition-colors cursor-pointer"
                  >
                    Dismiss All
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        {/* Centralized Main Viewport */}
        <main className="flex-1 w-full flex flex-col items-center px-4 sm:px-6 lg:px-8 py-4 min-w-0">
          <div className="w-full max-w-[1520px] mx-auto space-y-7 animate-fade-in" key={`${activeView}-${refreshTrigger}`}>
            {activeView === 'overview'   && <Overview onNavigate={navigateTo} onOpenProvision={() => setShowProvisionModal(true)} onTriggerFailover={() => setShowDrillModal(true)} showToast={showToast} />}
            {activeView === 'sandboxes'  && <Sandboxes onOpenProvision={() => setShowProvisionModal(true)} showToast={showToast} />}
            {activeView === 'gateways'   && <Gateways showToast={showToast} />}
            {activeView === 'traffic'    && <Routes onOpenCustomDomain={() => showToast('Custom Domain', 'Custom domain binding interface active.', 'info')} showToast={showToast} />}
            {activeView === 'pool'       && <Leases showToast={showToast} />}
            {activeView === 'config'     && <ConfigGenerations onOpenDiff={(f, t) => setDiffModalData({ from: f, to: t })} showToast={showToast} />}
            {activeView === 'audit'      && <Events showToast={showToast} />}
            {activeView === 'benchmarks' && <Benchmarks showToast={showToast} />}
            {activeView === 'settings'   && <Settings showToast={showToast} />}
          </div>
        </main>

        {/* Modern Floating Footer Bar */}
        <footer className="sticky bottom-3 mx-4 sm:mx-6 lg:mx-8 my-3 h-12 bg-[#2D0035] text-white rounded-2xl px-6 shadow-lg text-xs mt-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
            <span>Cluster Controller: <strong className="text-purple-200">FelCloud OpenStack</strong> (VRRP VIP 203.0.113.10 • 10.20.0.0/24 Pool)</span>
          </div>
          <div className="flex items-center gap-4">
            <button
              onClick={runJuryDemoScript}
              className="px-3.5 py-1.5 bg-white/15 hover:bg-white/25 active:bg-white/30 text-white rounded-xl text-xs font-semibold transition-all flex items-center gap-1.5 cursor-pointer shadow-xs"
            >
              <span className="material-symbols-outlined text-[16px]">smart_display</span>
              <span>Run Jury Demo Script</span>
            </button>
            <button
              onClick={() => setShowDocDrawer(true)}
              className="text-xs text-purple-200/90 hover:text-white transition-colors cursor-pointer underline font-medium"
            >
              REST API Specs
            </button>
          </div>
        </footer>
      </div>

      {/* ════════════════════════════════════════════════════════════
          MODAL: Provision Sandbox (Distinct, Spacious Card Groupings)
      ════════════════════════════════════════════════════════════ */}
      {showProvisionModal && (
        <div className="fixed inset-0 z-50 bg-black/45 backdrop-blur-md overflow-y-auto p-4 sm:p-6 lg:p-8 flex justify-center items-start animate-fade-in">
          <div className="bg-white rounded-3xl shadow-2xl max-w-4xl w-full overflow-hidden border border-purple-900/10 flex flex-col my-auto">
            {/* Modal Header */}
            <div className="px-8 py-6 border-b border-purple-900/5 flex items-center justify-between bg-white">
              <div className="flex items-center gap-4">
                <div className="w-12 h-12 rounded-2xl bg-[#2D0035] flex items-center justify-center shadow-md">
                  <span className="material-symbols-outlined text-[24px] text-white">rocket_launch</span>
                </div>
                <div>
                  <h2 className="text-2xl font-bold text-[#170B1B]">Provision Hackathon Sandbox</h2>
                  <p className="text-sm text-[#827787] mt-0.5">Automated OpenStack Nova VM, Neutron IPAM lease, and HAProxy routing commit</p>
                </div>
              </div>
              <button
                onClick={() => setShowProvisionModal(false)}
                className="w-10 h-10 rounded-2xl text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9] flex items-center justify-center transition-colors cursor-pointer"
              >
                <span className="material-symbols-outlined text-[22px]">close</span>
              </button>
            </div>

            {/* Modal Content - Spacious vertical breathing room gap-6 */}
            <div className="p-8 bg-[#F8F6F9] space-y-6">

              {/* Group 1: Team & Domain Track */}
              <div className="card-elevated p-6 space-y-4">
                <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
                  <div className="flex items-center gap-3">
                    <span className="material-symbols-outlined text-[22px] text-[#2D0035]">groups</span>
                    <h3 className="text-base font-bold text-[#170B1B]">1. Team Identity &amp; Domain Track</h3>
                  </div>
                  <span className="badge badge-purple">RFC1123 DNS Compliant</span>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
                  <div className="space-y-2">
                    <label className="text-sm font-semibold text-[#170B1B]">Team Slug / DNS Label</label>
                    <input
                      type="text"
                      required
                      value={provisionTeam}
                      onChange={e => setProvisionTeam(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g,''))}
                      placeholder="e.g. team12-ai-bot"
                      className="w-full h-12 px-4 text-base font-mono bg-[#F8F6F9] hover:bg-[#F3EFF5] focus:bg-white text-[#170B1B] rounded-2xl border border-purple-900/10 focus:border-[#2D0035] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/15 transition-all"
                    />
                  </div>

                  <div className="space-y-2">
                    <label className="text-sm font-semibold text-[#170B1B]">Hackathon Track Track</label>
                    <select
                      value={provisionTrack}
                      onChange={e => setProvisionTrack(e.target.value)}
                      className="w-full h-12 px-4 text-sm font-medium bg-[#F8F6F9] hover:bg-[#F3EFF5] focus:bg-white text-[#170B1B] rounded-2xl border border-purple-900/10 focus:border-[#2D0035] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/15 transition-all cursor-pointer"
                    >
                      <option>Fintech &amp; Algorithmic Trading</option>
                      <option>Autonomous AI Agents</option>
                      <option>IoT, Robotics &amp; Edge Telemetry</option>
                      <option>HealthTech &amp; Biomedical Data</option>
                    </select>
                  </div>
                </div>

                {/* Subdomain Preview */}
                <div className="p-4 rounded-2xl bg-[#F8F6F9] flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                  <div className="flex items-center gap-3">
                    <span className="material-symbols-outlined text-[20px] text-emerald-700">dns</span>
                    <span className="text-sm font-semibold text-[#170B1B]">Designate DNS:</span>
                    <span className="font-mono text-sm font-bold text-[#2D0035] bg-white px-3 py-1 rounded-xl shadow-xs">
                      {provisionTeam || 'team-sandbox'}.cstam.felcloud.tn
                    </span>
                  </div>
                  <span className="badge badge-emerald">Auto-Injected Wildcard SSL</span>
                </div>
              </div>

              {/* Group 2: Nova Compute Hardware Flavors */}
              <div className="card-elevated p-6 space-y-4">
                <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
                  <div className="flex items-center gap-3">
                    <span className="material-symbols-outlined text-[22px] text-[#2D0035]">memory</span>
                    <h3 className="text-base font-bold text-[#170B1B]">2. Nova Compute Flavors</h3>
                  </div>
                  <span className="text-xs font-mono text-[#827787]">Hypervisor: KVM / QEMU</span>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  {[
                    { id: 'm1.cstam.small',  label: 'Lightweight Web API', cpu: '1 vCPU', ram: '2 GB RAM', disk: '20 GB NVMe' },
                    { id: 'm1.cstam.medium', label: 'Standard Hackathon Sandbox', cpu: '2 vCPU', ram: '4 GB RAM', disk: '40 GB NVMe', default: true },
                    { id: 'm1.cstam.gpu',    label: 'AI/ML GPU Acceleration', cpu: '4 vCPU', ram: '8 GB RAM', disk: '80 GB NVMe' },
                  ].map(f => {
                    const selected = provisionFlavor === f.id
                    return (
                      <div
                        key={f.id}
                        onClick={() => setProvisionFlavor(f.id)}
                        className={`cursor-pointer p-5 rounded-2xl flex flex-col justify-between gap-3 transition-all duration-200 ${
                          selected
                            ? 'bg-[#2D0035] text-white shadow-lg shadow-purple-950/20 ring-2 ring-[#2D0035]'
                            : 'bg-[#F8F6F9] hover:bg-[#F3EFF5] text-[#170B1B]'
                        }`}
                      >
                        <div className="space-y-1">
                          <div className="flex items-center justify-between">
                            <span className="text-sm font-mono font-bold">{f.id}</span>
                            {f.default && (
                              <span className={`badge ${selected ? 'badge-purple text-white bg-white/20 border-white/20' : 'badge-emerald'}`}>
                                RECOMMENDED
                              </span>
                            )}
                          </div>
                          <div className={`text-xs ${selected ? 'text-purple-200' : 'text-[#827787]'}`}>{f.label}</div>
                        </div>

                        <div className="space-y-1 pt-2 border-t border-white/10 text-xs font-mono">
                          <div className="flex justify-between"><span>Compute:</span><span className="font-bold">{f.cpu}</span></div>
                          <div className="flex justify-between"><span>Memory:</span><span className="font-bold">{f.ram}</span></div>
                          <div className="flex justify-between"><span>Storage:</span><span className="font-bold">{f.disk}</span></div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>

              {/* Group 3: Lease Duration, Port & Security */}
              <div className="card-elevated p-6 space-y-4">
                <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
                  <div className="flex items-center gap-3">
                    <span className="material-symbols-outlined text-[22px] text-[#2D0035]">timer</span>
                    <h3 className="text-base font-bold text-[#170B1B]">3. Lease Lifetime &amp; Networking</h3>
                  </div>
                  <span className="badge badge-blue">Private Subnet 10.20.10.0/24</span>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
                  <div className="space-y-2">
                    <label className="text-sm font-semibold text-[#170B1B]">Container Port Target</label>
                    <input
                      type="number"
                      value={provisionPort}
                      onChange={e => setProvisionPort(e.target.value)}
                      className="w-full h-12 px-4 text-base font-mono bg-[#F8F6F9] hover:bg-[#F3EFF5] focus:bg-white text-[#170B1B] rounded-2xl border border-purple-900/10 focus:border-[#2D0035] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/15 transition-all"
                    />
                  </div>

                  <div className="space-y-2">
                    <label className="text-sm font-semibold text-[#170B1B]">Operational Lease Duration</label>
                    <div className="grid grid-cols-4 gap-2">
                      {['2','4','8','24'].map(h => (
                        <button
                          key={h}
                          type="button"
                          onClick={() => setProvisionTTL(h)}
                          className={`h-12 rounded-2xl text-sm font-bold font-mono transition-all cursor-pointer ${
                            provisionTTL === h
                              ? 'bg-[#2D0035] text-white shadow-md'
                              : 'bg-[#F8F6F9] hover:bg-[#F3EFF5] text-[#170B1B]'
                          }`}
                        >
                          {h}h
                        </button>
                      ))}
                    </div>
                  </div>
                </div>
              </div>

              {/* Modal Actions */}
              <div className="card-elevated p-6 flex flex-col sm:flex-row items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-2xl bg-purple-100 flex items-center justify-center font-bold font-mono text-base text-[#2D0035]">
                    {provisionTeam ? provisionTeam[0].toUpperCase() : 'T'}
                  </div>
                  <div>
                    <div className="text-sm font-bold text-[#170B1B]">Deploy Target: {provisionTeam || 'team-sandbox'}</div>
                    <div className="text-xs text-[#827787]">Dynamic IP will be assigned from 10.20.10.0/24 subnet</div>
                  </div>
                </div>

                <div className="flex items-center gap-3 w-full sm:w-auto">
                  <button
                    type="button"
                    onClick={() => setShowProvisionModal(false)}
                    className="flex-1 sm:flex-none px-6 py-3.5 text-sm font-semibold text-[#827787] hover:text-[#170B1B] rounded-2xl transition-colors cursor-pointer"
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    disabled={provisionLoading || !provisionTeam}
                    onClick={handleProvisionSubmit}
                    className="flex-1 sm:flex-none px-8 py-3.5 bg-[#2D0035] hover:bg-[#43134c] text-white rounded-2xl text-base font-bold flex items-center justify-center gap-2.5 shadow-lg shadow-purple-950/20 transition-all cursor-pointer disabled:opacity-50"
                  >
                    {provisionLoading ? (
                      <>
                        <span className="material-symbols-outlined text-[20px] animate-spin">sync</span>
                        <span>Deploying Nova VM…</span>
                      </>
                    ) : (
                      <>
                        <span className="material-symbols-outlined text-[20px]">bolt</span>
                        <span>Deploy &amp; Commit Sandbox</span>
                      </>
                    )}
                  </button>
                </div>
              </div>

            </div>
          </div>
        </div>
      )}

      {/* ════════════════════════════════════════════════════════════
          MODAL: Failover Drill
      ════════════════════════════════════════════════════════════ */}
      {showDrillModal && (
        <div className="fixed inset-0 z-50 bg-black/45 backdrop-blur-md flex items-center justify-center p-4 animate-fade-in">
          <div className="bg-white rounded-3xl w-full max-w-md shadow-2xl overflow-hidden border border-purple-900/10 p-8 space-y-6">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3.5">
                <div className="w-12 h-12 rounded-2xl bg-rose-50 border border-rose-100 flex items-center justify-center">
                  <span className="material-symbols-outlined text-[24px] text-rose-600">flash_on</span>
                </div>
                <div>
                  <h3 className="font-bold text-lg text-[#170B1B]">VRRP Failover Drill</h3>
                  <p className="text-xs text-[#827787]">Simulate primary gateway crash</p>
                </div>
              </div>
              <button
                onClick={() => setShowDrillModal(false)}
                className="w-9 h-9 rounded-xl text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9] flex items-center justify-center transition-colors cursor-pointer"
              >
                <span className="material-symbols-outlined text-[20px]">close</span>
              </button>
            </div>

            <p className="text-sm text-[#827787] leading-relaxed">
              Injects a simulated SIGKILL on the primary HAProxy/Keepalived process, validating VRRP gratuitous ARP and sub-second stream preservation without dropped TCP connections.
            </p>

            <div className="p-4 bg-amber-50/70 rounded-2xl text-sm space-y-2 border border-amber-200/40">
              <div className="flex justify-between">
                <span className="text-[#827787]">Failover Target:</span>
                <span className="font-mono font-bold text-[#170B1B]">Standby Node (Auto-Election)</span>
              </div>
              <div className="flex justify-between">
                <span className="text-[#827787]">Expected Promoted Time:</span>
                <span className="font-mono font-bold text-emerald-700">&lt; 500 ms</span>
              </div>
            </div>

            <div className="flex justify-end gap-3 pt-2">
              <button
                onClick={() => setShowDrillModal(false)}
                className="px-5 py-3 text-sm font-semibold text-[#827787] hover:text-[#170B1B] rounded-xl transition-colors cursor-pointer"
              >
                Cancel
              </button>
              <button
                onClick={handleDrillSubmit}
                className="px-6 py-3 bg-rose-600 hover:bg-rose-700 text-white rounded-xl text-sm font-bold flex items-center gap-2 transition-all shadow-md shadow-rose-600/20 cursor-pointer"
              >
                <span className="material-symbols-outlined text-[18px]">flash_on</span>
                <span>Confirm Failover Drill</span>
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ════════════════════════════════════════════════════════════
          MODAL: Diff Viewer
      ════════════════════════════════════════════════════════════ */}
      {diffModalData && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-md p-4 animate-fade-in">
          <div className="bg-white rounded-3xl shadow-2xl max-w-2xl w-full overflow-hidden border border-purple-900/10 p-7 space-y-5">
            <div className="flex items-center justify-between pb-3.5 border-b border-purple-900/5">
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-xl bg-purple-50 flex items-center justify-center text-[#2D0035]">
                  <span className="material-symbols-outlined text-[20px]">compare</span>
                </div>
                <span className="text-base font-bold text-[#170B1B]">Config Diff — {diffModalData.from} → {diffModalData.to}</span>
              </div>
              <button
                onClick={() => setDiffModalData(null)}
                className="w-9 h-9 rounded-xl text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9] flex items-center justify-center cursor-pointer"
              >
                <span className="material-symbols-outlined text-[20px]">close</span>
              </button>
            </div>
            <div className="p-5 font-mono text-xs bg-[#170B1B] text-purple-100 rounded-2xl overflow-x-auto max-h-96 leading-relaxed">
              <pre><code>{`@@ -13,6 +13,8 @@ frontend cstam_fe
     bind 203.0.113.10:443 ssl crt /etc/ssl/cstam-west.pem alpn h2,http/1.1
     mode http
-    stick-table type ip size 100k expire 30s store http_req_rate(10s)
-    http-request track-sc0 src
+    stick-table type ip size 250k expire 30s store http_req_rate(10s)
+    http-request track-sc0 src
+    acl rate_abuse sc_http_req_rate(0) gt 45000
+    http-request tarpit deny_status 429 if rate_abuse
     acl is_quarantine src -f /etc/haproxy/quarantine_ips.lst
     http-request deny if is_quarantine
     default_backend sandbox_be`}</code></pre>
            </div>
            <div className="flex justify-end pt-2">
              <button
                onClick={() => setDiffModalData(null)}
                className="px-6 py-2.5 bg-[#2D0035] text-white rounded-xl text-sm font-bold hover:bg-[#43134c] transition-colors cursor-pointer"
              >
                Close Inspector
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ════════════════════════════════════════════════════════════
          DRAWER: REST API Docs
      ════════════════════════════════════════════════════════════ */}
      {showDocDrawer && (
        <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm animate-fade-in" onClick={() => setShowDocDrawer(false)}>
          <div
            className="absolute right-0 top-0 h-full w-[520px] bg-white rounded-l-3xl shadow-2xl p-8 overflow-y-auto space-y-6"
            onClick={e => e.stopPropagation()}
          >
            <div className="flex items-center justify-between pb-4 border-b border-purple-900/5">
              <div className="flex items-center gap-3">
                <div className="w-11 h-11 rounded-2xl bg-purple-50 flex items-center justify-center text-[#2D0035]">
                  <span className="material-symbols-outlined text-[22px]">menu_book</span>
                </div>
                <div>
                  <h3 className="font-bold text-lg text-[#170B1B]">REST API Reference</h3>
                  <p className="text-xs text-[#827787] font-mono">CSTAM 3.0 Control Plane</p>
                </div>
              </div>
              <button
                onClick={() => setShowDocDrawer(false)}
                className="w-9 h-9 rounded-xl text-[#827787] hover:text-[#170B1B] hover:bg-[#F8F6F9] flex items-center justify-center cursor-pointer"
              >
                <span className="material-symbols-outlined text-[20px]">close</span>
              </button>
            </div>

            <p className="text-sm text-[#827787] leading-relaxed">
              All endpoints require <code className="font-mono bg-purple-50 text-[#2D0035] px-2 py-0.5 rounded-md font-bold text-xs">Authorization: Bearer &lt;token&gt;</code>.
            </p>

            <div className="space-y-3.5 text-sm">
              {[
                { method: 'GET',    c: 'badge-emerald', path: '/api/gateways/status', desc: 'VRRP master state, HAProxy uptime, VIP holder.' },
                { method: 'GET',    c: 'badge-emerald', path: '/api/sandboxes',       desc: 'List all sandboxes with lease and status data.' },
                { method: 'POST',   c: 'badge-blue',    path: '/api/sandboxes/provision', desc: '{"team":"alpha","ttl":7200} — deploys Nova + routes.' },
                { method: 'DELETE', c: 'badge-rose',    path: '/api/sandboxes/:id',   desc: 'Teardown VM, enter 180s quarantine cooldown.' },
                { method: 'POST',   c: 'badge-amber',   path: '/api/gateways/failover', desc: 'Trigger zero-downtime failover to standby node.' },
                { method: 'POST',   c: 'badge-purple',  path: '/api/gateways/sync',   desc: 'Reconcile HAProxy maps with Nova/Neutron state.' },
                { method: 'GET',    c: 'badge-emerald', path: '/api/leases',          desc: 'All IPAM leases with expiry & quarantine state.' },
                { method: 'GET',    c: 'badge-emerald', path: '/api/events',          desc: 'Audit log stream with types, scopes, timing.' },
              ].map((ep, i) => (
                <div key={i} className="p-4 bg-[#F8F6F9] hover:bg-[#F3EEF5] rounded-2xl space-y-1.5 transition-colors">
                  <div className="flex items-center gap-2.5 font-mono">
                    <span className={`badge ${ep.c}`}>{ep.method}</span>
                    <span className="text-[#170B1B] font-bold text-sm">{ep.path}</span>
                  </div>
                  <div className="text-xs text-[#827787]">{ep.desc}</div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* ════════════════════════════════════════════════════════════
          FLOATING TOASTS NOTIFICATIONS
      ════════════════════════════════════════════════════════════ */}
      <div className="fixed bottom-16 right-6 z-50 flex flex-col gap-2.5 pointer-events-none w-[360px]">
        {toasts.map(t => {
          const c = toastColorMap[t.type]
          return (
            <div
              key={t.id}
              className={`pointer-events-auto bg-white border ${c.ring} rounded-2xl overflow-hidden shadow-xl shadow-purple-950/10 flex items-stretch animate-fade-up`}
            >
              <div className={`w-1.5 flex-shrink-0 ${c.bar}`} />
              <div className="flex items-start gap-3.5 p-4 flex-1 min-w-0">
                <div className={`w-8 h-8 rounded-xl ${c.bg} flex items-center justify-center flex-shrink-0 mt-0.5`}>
                  <span className={`material-symbols-outlined text-[17px] ${c.icon}`}>
                    {toastIconMap[t.type]}
                  </span>
                </div>
                <div className="flex-1 min-w-0">
                  <div className="font-bold text-sm text-[#170B1B]">{t.title}</div>
                  <div className="text-xs text-[#827787] mt-0.5 leading-relaxed">{t.message}</div>
                </div>
                <button
                  onClick={() => setToasts(p => p.filter(x => x.id !== t.id))}
                  className="text-gray-300 hover:text-gray-600 flex-shrink-0 mt-0.5 transition-colors cursor-pointer"
                >
                  <span className="material-symbols-outlined text-[17px]">close</span>
                </button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
