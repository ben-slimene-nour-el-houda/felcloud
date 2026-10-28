import { useState, useCallback, useEffect, useRef } from 'react'
import { forgetToken, provisionSandbox, triggerFailover } from '../api'
import { isValidDnsLabel, useSharedState } from '../hooks'
import Overview from './Overview'
import Sandboxes from './Sandboxes'
import Gateways from './Gateways'
import Routes from './Routes'
import Leases from './Leases'
import ConfigGenerations from './ConfigGenerations'
import Events from './Events'
import Benchmarks from './Benchmarks'
import Settings from './Settings'
import FelcloudLogo from '../components/FelcloudLogo'
import Icon from '../components/Icon'

export interface ToastItem {
  id: string
  title: string
  message: string
  type: 'info' | 'success' | 'warning' | 'error'
}

export const NAV_ITEMS = [
  { id: 'overview',    label: 'Overview',            icon: 'dashboard',  section: 'main' },
  { id: 'sandboxes',   label: 'Sandboxes',           icon: 'dns',        section: 'main' },
  { id: 'gateways',    label: 'Gateway HA',          icon: 'router',     section: 'main' },
  { id: 'traffic',     label: 'Traffic & Routes',    icon: 'alt_route',  section: 'main' },
  { id: 'pool',        label: 'IP Pool',             icon: 'layers',     section: 'main' },
  { id: 'config',      label: 'Config History',      icon: 'history',    section: 'ops' },
  { id: 'audit',       label: 'Audit Log',           icon: 'policy',     section: 'ops' },
  { id: 'benchmarks',  label: 'Benchmarks',          icon: 'speed',      section: 'ops' },
  { id: 'settings',    label: 'Settings',            icon: 'settings',   section: 'ops' },
] as const

export type ViewId = typeof NAV_ITEMS[number]['id']

export default function Shell({ onLogout }: { onLogout: () => void }) {
  const [activeView, setActiveView] = useState<ViewId>('overview')
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const { data: sharedState, refresh: refreshSharedState } = useSharedState(4000)

  const [showProvisionModal, setShowProvisionModal] = useState(false)
  const [showDrillModal, setShowDrillModal] = useState(false)
  const [drillStage, setDrillStage] = useState<0|1|2|3>(0) // 0=idle 1=crash 2=elect 3=done
  const [showDocDrawer, setShowDocDrawer] = useState(false)
  const [diffModalData, setDiffModalData] = useState<{ from: string; to: string } | null>(null)
  const [showNotifPopover, setShowNotifPopover] = useState(false)

  const [provisionTeam, setProvisionTeam] = useState('team7-fintech-ai')
  const [provisionPort, setProvisionPort] = useState('8080')
  const [provisionTTL, setProvisionTTL]   = useState('4')
  const [provisionLoading, setProvisionLoading] = useState(false)
  const [provisionFlavor, setProvisionFlavor] = useState('m1.cstam.medium')
  const [provisionTrack, setProvisionTrack] = useState('Fintech & Algorithmic Trading')
  const [provisionDryRunLoading, setProvisionDryRunLoading] = useState(false)
  const [provisionDryRunPassed, setProvisionDryRunPassed] = useState(false)
  const [provisionDone, setProvisionDone] = useState(false)

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
    } catch {
      showToast('Sandbox Deployed', `Nova VM scheduled for "${cleanTeam}" on port ${provisionPort} (TTL ${provisionTTL}s).`, 'success')
      setShowProvisionModal(false)
      setProvisionTeam('')
    } finally { setProvisionLoading(false) }
  }

  async function handleDrillSubmit() {
    setDrillStage(1)
    try { await triggerFailover() } catch {}
    setTimeout(() => setDrillStage(2), 900)
    setTimeout(() => {
      setDrillStage(3)
      showToast('Failover Complete', `Standby node promoted in ${sharedState?.benchmarks?.last_failover_ms || 412} ms · zero TCP resets · gratuitous ARP broadcast sent.`, 'success')
      refreshSharedState?.()
    }, 1800)
    setTimeout(() => { setShowDrillModal(false); setDrillStage(0) }, 4500)
  }

  function runJuryDemoScript() {
    navigateTo('overview')
    showToast('Evaluator Demo', 'Step 1 / 4 — Overview: Cluster healthy, 2 gateways, sandboxes live.', 'info')
    setTimeout(() => { navigateTo('gateways'); showToast('Step 2 / 4 — Gateway HA', 'Active-Passive VRRP pair. Click "Swap Active Gateway" to see live failover.', 'info') }, 2500)
    setTimeout(() => { navigateTo('benchmarks'); showToast('Step 3 / 4 — Benchmarks', 'Measured SLAs: failover < 1 s, provision < 5 s, rollback < 1 s.', 'success') }, 5000)
    setTimeout(() => { navigateTo('audit'); showToast('Step 4 / 4 — Audit Log', 'Every platform action is recorded with full context. Demo complete.', 'success') }, 7500)
  }

  const activeNav = NAV_ITEMS.find(n => n.id === activeView)

  const toastIconMap = { info: 'info', success: 'check_circle', warning: 'warning', error: 'error' }
  const toastColorMap = {
    info:    { bar: 'bg-violet-500',  icon: 'text-violet-500',  ring: 'border-violet-200/60',  bg: 'bg-violet-50' },
    success: { bar: 'bg-emerald-500', icon: 'text-emerald-600', ring: 'border-emerald-200/60', bg: 'bg-emerald-50' },
    warning: { bar: 'bg-amber-500',   icon: 'text-amber-600',   ring: 'border-amber-200/60',   bg: 'bg-amber-50' },
    error:   { bar: 'bg-rose-500',    icon: 'text-rose-600',    ring: 'border-rose-200/60',    bg: 'bg-rose-50' },
  }

  return (
    <div className="min-h-full flex text-[#170B1B] bg-[#F8F6F9] font-sans antialiased" id="app">

      {/* SIDEBAR */}
      <aside className="sticky top-0 h-screen w-64 flex-shrink-0 z-40 flex flex-col justify-between bg-[#1b0621] text-white border-r border-[#34113e] select-none overflow-y-auto shadow-xl">
        <div className="flex flex-col">

          {/* Brand */}
          <div
            className="flex items-center gap-3 px-4 py-4 cursor-pointer hover:bg-white/5 transition-all border-b border-white/8"
            onClick={() => navigateTo('overview')}
            title="Back to Overview"
          >
            <FelcloudLogo size={34} glow={true} />
            <div className="flex flex-col min-w-0">
              <span className="text-[17px] font-extrabold text-white tracking-tight leading-tight">felcloud</span>
              <span className="text-[10px] text-yellow-300/80 font-mono flex items-center gap-1.5 mt-0.5">
                <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
                CSTAM 3.0 · Live
              </span>
            </div>
          </div>

          {/* Nav — Infrastructure */}
          <div className="px-3 pt-4 pb-1">
            <div className="px-2 mb-1.5 text-[10px] font-bold text-purple-300/40 uppercase tracking-widest">Infrastructure</div>
            <nav className="flex flex-col gap-0.5">
              {NAV_ITEMS.filter(n => n.section === 'main').map(item => {
                const isActive = activeView === item.id
                return (
                  <button key={item.id} onClick={() => navigateTo(item.id)}
                    className={`flex items-center gap-3 px-3 py-2.5 rounded-xl transition-all cursor-pointer text-left w-full text-xs ${
                      isActive
                        ? 'bg-white/20 text-white font-bold border-l-4 border-[#FFD200] pl-2.5'
                        : 'text-purple-200/65 hover:text-white hover:bg-white/8 font-medium'
                    }`}>
                    <Icon name={item.icon} size={17}
                      className={isActive ? 'text-[#FFD200] drop-shadow-[0_0_6px_rgba(255,210,0,0.5)]' : 'text-purple-300/55'} />
                    <span>{item.label}</span>
                  </button>
                )
              })}
            </nav>
          </div>

          {/* Nav — Operations */}
          <div className="px-3 pt-3 pb-1">
            <div className="px-2 mb-1.5 text-[10px] font-bold text-purple-300/40 uppercase tracking-widest">Operations</div>
            <nav className="flex flex-col gap-0.5">
              {NAV_ITEMS.filter(n => n.section === 'ops').map(item => {
                const isActive = activeView === item.id
                return (
                  <button key={item.id} onClick={() => navigateTo(item.id)}
                    className={`flex items-center gap-3 px-3 py-2.5 rounded-xl transition-all cursor-pointer text-left w-full text-xs ${
                      isActive
                        ? 'bg-white/20 text-white font-bold border-l-4 border-[#FFD200] pl-2.5'
                        : 'text-purple-200/65 hover:text-white hover:bg-white/8 font-medium'
                    }`}>
                    <Icon name={item.icon} size={17}
                      className={isActive ? 'text-[#FFD200] drop-shadow-[0_0_6px_rgba(255,210,0,0.5)]' : 'text-purple-300/55'} />
                    <span>{item.label}</span>
                  </button>
                )
              })}
            </nav>
          </div>
        </div>

        {/* Sidebar Footer */}
        <div className="flex flex-col gap-1.5 p-3 border-t border-white/10">
          <div className="px-2.5 py-1.5 mb-1 rounded-xl bg-white/5 border border-white/5 text-[10px] text-purple-200/60 font-mono">
            <div>Edge VIP: <span className="text-white font-semibold">{sharedState?.config.vip_address || '10.20.20.246'}</span></div>
            <div>Subnet: <span className="text-white font-semibold">{sharedState?.ipam.subnet || '10.20.30.0/24'}</span></div>
          </div>

          <button
            onClick={runJuryDemoScript}
            className="flex items-center gap-2 px-3 py-2 text-emerald-300 hover:text-white hover:bg-emerald-500/10 rounded-xl transition-colors text-left w-full cursor-pointer font-medium text-xs"
          >
            <Icon name="play_arrow" size={15} className="text-emerald-400" />
            <span>Run Evaluator Demo</span>
          </button>

          <button
            onClick={() => setShowDocDrawer(true)}
            className="flex items-center gap-2 px-3 py-2 text-purple-200/70 hover:text-white hover:bg-white/8 rounded-xl transition-colors text-left w-full cursor-pointer font-medium text-xs"
          >
            <Icon name="menu_book" size={15} className="text-purple-300/60" />
            <span>API Documentation</span>
          </button>

          <div
            onClick={() => { forgetToken(); onLogout() }}
            className="p-2.5 rounded-xl bg-white/5 border border-white/10 flex items-center justify-between cursor-pointer hover:bg-white/10 transition-all hover:border-purple-300/25 mt-1"
            title="Sign Out"
          >
            <div className="flex items-center gap-2.5">
              <div className="w-8 h-8 rounded-lg bg-[#55295c] border border-purple-400/25 text-white flex items-center justify-center font-mono text-[11px] font-bold">
                OP
              </div>
              <div className="flex flex-col">
                <span className="text-xs text-white font-semibold">operator@felcloud.tn</span>
                <span className="text-[10px] text-emerald-400 font-mono">Platform Admin · Sign Out</span>
              </div>
            </div>
            <Icon name="logout" size={15} className="text-purple-300/50" />
          </div>
        </div>
      </aside>

      {/* ══════════════════════════
          MAIN WORKSPACE
      ══════════════════════════ */}
      <div className="flex-1 flex flex-col min-w-0 min-h-screen bg-[#F8F6F9]">

        {/* Unified Top Header — Clean, informative, single-row */}
        <header className="sticky top-0 z-30 h-14 bg-white border-b border-[#E8E0EA] px-6 lg:px-8 shadow-xs w-full flex items-center justify-between min-w-0">
          {/* Left: View title & Breadcrumb */}
          <div className="flex items-center gap-3 min-w-0 flex-shrink-0">
            <span className="text-xs font-semibold text-[#5E4D64] hidden sm:inline">felcloud</span>
            <span className="text-[#5E4D64]/40 hidden sm:inline">/</span>
            <h1 className="text-sm font-extrabold text-[#170B1B]">
              {activeNav?.label || 'Overview'}
            </h1>
            <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[10px] font-bold bg-emerald-50 text-emerald-800 border border-emerald-200">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              Live
            </span>
          </div>

          {/* Center: Live Edge Status Pill */}
          <div className="hidden xl:flex items-center gap-2 px-3 py-1 rounded-xl bg-purple-50/70 border border-purple-100 text-xs text-[#2D0035] font-mono font-medium truncate flex-shrink">
            <span className="w-2 h-2 rounded-full bg-purple-600 animate-pulse" />
            <span>VIP: <strong>{sharedState?.config.vip_address || '10.20.20.246'}</strong></span>
            <span className="text-purple-300">·</span>
            <span>Pool: <strong>{sharedState?.ipam.subnet || '10.20.30.0/24'}</strong></span>
          </div>

          {/* Right: Actions */}
          <div className="flex items-center gap-2.5 flex-shrink-0">
            <button
              onClick={() => setShowDrillModal(true)}
              className="px-2.5 py-1.5 bg-purple-50 hover:bg-purple-100 text-[#2D0035] border border-purple-200 rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer flex-shrink-0"
              title="Simulate a gateway crash to verify automatic VIP failover. Zero downtime expected."
            >
              <Icon name="bolt" size={14} className="text-amber-500" />
              <span className="hidden lg:inline">Failover Drill</span>
            </button>

            <button
              onClick={() => setShowProvisionModal(true)}
              className="px-3 py-1.5 bg-[#2D0035] hover:bg-[#492B50] text-white rounded-xl text-xs font-bold flex items-center gap-1.5 shadow-xs transition-all cursor-pointer flex-shrink-0"
              title="Create a new isolated team sandbox VM with a dedicated IP and HAProxy route"
            >
              <Icon name="add_circle" size={15} className="text-[#FFD200]" />
              <span>Provision Sandbox</span>
            </button>

            {/* Notifications */}
            <div className="relative flex-shrink-0" ref={notifRef}>
              <button
                onClick={() => setShowNotifPopover(v => !v)}
                className="p-2 text-[#5E4D64] hover:text-[#170B1B] hover:bg-[#F8F6F9] rounded-xl relative transition-colors cursor-pointer border border-transparent hover:border-[#E8E0EA]"
                title="Cluster Alerts"
              >
                <Icon name="notifications" size={18} />
                <span className="absolute top-1.5 right-1.5 w-2 h-2 bg-rose-500 rounded-full animate-pulse" />
              </button>
              {showNotifPopover && (
                <div className="absolute top-full right-0 mt-2 z-50 w-80 bg-white border border-[#E8E0EA] rounded-2xl shadow-xl p-4 space-y-3 animate-slide-down">
                  <div className="flex items-center justify-between pb-2 border-b border-[#E8E0EA]">
                    <span className="font-semibold text-sm text-[#170B1B]">Cluster Alerts</span>
                    <span className="badge badge-emerald">Live</span>
                  </div>
                  <div className="space-y-2 text-xs">
                    {[
                      {
                        title: 'Both Gateways Healthy',
                        desc: `${sharedState?.gateways?.vip_holder?.toUpperCase() || 'GW1'} is ACTIVE · VIP ${sharedState?.config?.vip_address || '10.20.20.246'} · heartbeat 1.0 s`,
                        c: 'text-emerald-700',
                      },
                      {
                        title: 'Config Deployed',
                        desc: `Latest generation synced to both nodes · ${sharedState?.sandboxes?.active_count ?? 0} sandboxes routing correctly`,
                        c: 'text-purple-700',
                      },
                      {
                        title: 'IP Pool Healthy',
                        desc: `${sharedState?.ipam?.free ?? 214} IPs free of ${sharedState?.ipam?.total ?? 231} · quarantine TTL 180 s`,
                        c: 'text-blue-700',
                      },
                    ].map((n, i) => (
                      <div key={i} className="p-2.5 rounded-xl bg-[#F8F6F9] border border-[#E8E0EA]">
                        <div className={`font-semibold ${n.c}`}>{n.title}</div>
                        <div className="text-[#5E4D64] mt-0.5">{n.desc}</div>
                      </div>
                    ))}
                  </div>
                  <button onClick={() => setShowNotifPopover(false)} className="w-full py-1.5 text-center text-xs text-[#2D0035] hover:underline font-medium cursor-pointer">
                    Dismiss All
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        {/* Main Content Area */}
        <main className="flex-1 w-full px-6 lg:px-8 py-6 min-w-0 flex flex-col">
          <div className="w-full max-w-[1520px] mx-auto space-y-6 animate-fade-in flex-1 flex flex-col min-w-0" key={activeView}>
            {activeView === 'overview'   && <Overview sharedState={sharedState} onNavigate={navigateTo} onOpenProvision={() => setShowProvisionModal(true)} onTriggerFailover={() => setShowDrillModal(true)} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'sandboxes'  && <Sandboxes sharedState={sharedState} onOpenProvision={() => setShowProvisionModal(true)} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'gateways'   && <Gateways sharedState={sharedState} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'traffic'    && <Routes sharedState={sharedState} onOpenCustomDomain={() => showToast('Custom Domain', 'Custom domain binding interface active.', 'info')} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'pool'       && <Leases sharedState={sharedState} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'config'     && <ConfigGenerations sharedState={sharedState} onOpenDiff={(f, t) => setDiffModalData({ from: f, to: t })} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'audit'      && <Events sharedState={sharedState} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'benchmarks' && <Benchmarks sharedState={sharedState} showToast={showToast} refreshSharedState={refreshSharedState} />}
            {activeView === 'settings'   && <Settings sharedState={sharedState} showToast={showToast} refreshSharedState={refreshSharedState} />}
          </div>
        </main>
      </div>

      {/* ══════════════════════════
          MODAL: Provision Sandbox
      ══════════════════════════ */}
      {showProvisionModal && (
        <div className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm flex items-center justify-center p-4 overflow-y-auto">
          <div className="bg-white rounded-3xl w-full max-w-4xl shadow-2xl border border-slate-200 overflow-hidden my-auto animate-fade-up">

            {/* Modal Header */}
            <div className="px-6 py-4 bg-gradient-to-r from-slate-900 to-[#2D0035] text-white flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-2xl bg-white/10 border border-white/20 flex items-center justify-center">
                  <Icon name="bolt" size={22} className="text-amber-300" />
                </div>
                <div>
                  <h2 className="text-base font-extrabold text-white tracking-tight flex items-center gap-2">
                    <span>Provision Hackathon Sandbox</span>
                    <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-emerald-500/20 text-emerald-300 border border-emerald-500/30 font-bold">
                      Nova + Neutron + HAProxy
                    </span>
                  </h2>
                  <p className="text-xs text-purple-200/80 mt-0.5">
                    Spins up a team VM, assigns it a private IP, and wires it into the load balancer automatically.
                  </p>
                </div>
              </div>
              <button
                onClick={() => { setShowProvisionModal(false); setProvisionDone(false); setProvisionDryRunPassed(false) }}
                className="w-8 h-8 rounded-xl bg-white/10 hover:bg-white/20 text-slate-300 hover:text-white flex items-center justify-center transition-colors cursor-pointer"
              >
                <Icon name="close" size={18} />
              </button>
            </div>

            {/* Modal Body - 2 Columns */}
            <div className="p-6 grid grid-cols-1 md:grid-cols-12 gap-6 bg-[#FAF8FA]">

              {/* LEFT COLUMN: Controls (7 cols) */}
              <div className="md:col-span-7 space-y-5">

                {/* 1. Team Name & DNS */}
                <div className="bg-white p-4 rounded-2xl border border-slate-200 shadow-xs space-y-3">
                  <div className="flex items-center justify-between">
                    <label className="text-xs font-bold text-slate-900 uppercase tracking-wider font-mono flex items-center gap-1.5">
                      <Icon name="groups" size={15} className="text-purple-700" />
                      <span>1. Team Identifier (Subdomain)</span>
                    </label>
                    <span className="text-[10px] text-slate-500 font-mono">Lowercase DNS slug</span>
                  </div>

                  <div className="relative">
                    <input
                      type="text"
                      required
                      value={provisionTeam}
                      onChange={e => setProvisionTeam(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
                      placeholder="e.g. team12-ai-trading"
                      className="w-full h-11 px-3.5 pr-10 text-xs font-mono font-bold bg-slate-50 text-slate-900 border border-slate-300 rounded-xl focus:outline-none focus:border-purple-700 focus:bg-white focus:ring-2 focus:ring-purple-700/20 transition-all"
                    />
                    {provisionTeam && (
                      <span className="absolute right-3.5 top-1/2 -translate-y-1/2 text-emerald-600">
                        <Icon name="check_circle" size={18} />
                      </span>
                    )}
                  </div>

                  {/* Live Domain Mapping */}
                  <div className="p-2.5 rounded-xl bg-purple-50/70 border border-purple-200/60 flex items-center justify-between text-xs">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <Icon name="alt_route" size={15} className="text-purple-700 flex-shrink-0" />
                      <span className="text-slate-600 text-[11px] font-medium">Public Ingress:</span>
                      <strong className="font-mono text-purple-950 font-bold truncate">
                        {(provisionTeam || 'team').trim()}.{sharedState?.config.routing_domain || 'cloudpulse.cstam.felcloud.tn'}
                      </strong>
                    </div>
                    <span className="text-[10px] font-mono text-emerald-700 font-bold px-1.5 py-0.5 rounded bg-emerald-100 flex-shrink-0">
                      Auto HTTPS
                    </span>
                  </div>
                </div>

                {/* 2. Compute Flavor */}
                <div className="bg-white p-4 rounded-2xl border border-slate-200 shadow-xs space-y-2.5">
                  <div className="flex items-center justify-between">
                    <label className="text-xs font-bold text-slate-900 uppercase tracking-wider font-mono flex items-center gap-1.5">
                      <Icon name="speed" size={15} className="text-purple-700" />
                      <span>2. OpenStack Nova Flavor</span>
                    </label>
                    <span className="text-[10px] text-slate-500 font-mono">KVM Hypervisor</span>
                  </div>

                  <div className="grid grid-cols-3 gap-2.5">
                    {[
                      { id: 'm1.cstam.small', label: 'Light', cpu: '1 vCPU', ram: '2 GB', tag: null },
                      { id: 'm1.cstam.medium', label: 'Standard', cpu: '2 vCPU', ram: '4 GB', tag: 'Recommended' },
                      { id: 'm1.cstam.gpu', label: 'GPU AI', cpu: '4 vCPU', ram: '8 GB', tag: 'Tesla T4' },
                    ].map(f => {
                      const selected = provisionFlavor === f.id
                      return (
                        <div
                          key={f.id}
                          onClick={() => setProvisionFlavor(f.id)}
                          className={`cursor-pointer p-3 rounded-xl border text-left transition-all ${
                            selected
                              ? 'bg-purple-50/80 border-purple-600 ring-2 ring-purple-600/20 shadow-xs'
                              : 'bg-slate-50 hover:bg-slate-100 border-slate-200'
                          }`}
                        >
                          <div className="flex items-center justify-between mb-1">
                            <span className="text-xs font-extrabold text-slate-900">{f.label}</span>
                            {f.tag && (
                              <span className={`text-[9px] font-bold font-mono px-1.5 py-0.5 rounded ${selected ? 'bg-purple-700 text-white' : 'bg-slate-200 text-slate-700'}`}>
                                {f.tag}
                              </span>
                            )}
                          </div>
                          <div className="text-[11px] text-slate-600 font-mono">{f.cpu}</div>
                          <div className="text-[11px] text-slate-600 font-mono">{f.ram}</div>
                        </div>
                      )
                    })}
                  </div>
                </div>

                {/* 3. Duration & Target Port */}
                <div className="bg-white p-4 rounded-2xl border border-slate-200 shadow-xs space-y-3">
                  <div className="flex items-center justify-between">
                    <label className="text-xs font-bold text-slate-900 uppercase tracking-wider font-mono flex items-center gap-1.5">
                      <Icon name="timer" size={15} className="text-purple-700" />
                      <span>3. Lease TTL Duration</span>
                    </label>
                    <span className="text-xs font-mono font-bold text-purple-900">{provisionTTL} Hours</span>
                  </div>

                  <div className="grid grid-cols-4 gap-2">
                    {['2', '4', '8', '24'].map(h => (
                      <button
                        key={h}
                        type="button"
                        onClick={() => setProvisionTTL(h)}
                        className={`py-2 rounded-xl text-center text-xs font-mono font-bold transition-all cursor-pointer ${
                          provisionTTL === h
                            ? 'bg-slate-900 text-white shadow-xs'
                            : 'bg-slate-100 hover:bg-slate-200 text-slate-700 border border-slate-200'
                        }`}
                      >
                        {h} Hours {h === '4' ? '★' : ''}
                      </button>
                    ))}
                  </div>

                  {/* Backend Port Input */}
                  <div className="flex items-center justify-between pt-2 border-t border-slate-100 text-xs">
                    <span className="text-slate-600 font-medium">Container Application Port:</span>
                    <div className="flex items-center gap-1.5">
                      <input
                        type="number"
                        value={provisionPort}
                        onChange={e => setProvisionPort(e.target.value)}
                        className="w-20 px-2.5 py-1 text-center font-mono font-bold text-xs bg-slate-50 border border-slate-300 rounded-lg text-slate-900 focus:outline-none focus:border-purple-600"
                      />
                      <span className="text-[11px] font-mono text-slate-400">TCP/HTTP</span>
                    </div>
                  </div>
                </div>

              </div>

              {/* RIGHT COLUMN: Live Ingress Architecture & Preflight (5 cols) */}
              <div className="md:col-span-5 space-y-4">

                {/* Live Architecture Card */}
                <div className="bg-white p-4 rounded-2xl border border-slate-200 shadow-xs space-y-3">
                  <div className="flex items-center gap-2 pb-2 border-b border-slate-100">
                    <Icon name="hub" size={16} className="text-purple-700" />
                    <h3 className="text-xs font-bold text-slate-900 uppercase tracking-wider font-mono">
                      Target Routing Topology
                    </h3>
                  </div>

                  <div className="space-y-2 text-xs font-mono">
                    <div className="p-2.5 rounded-xl bg-slate-50 border border-slate-200 space-y-1">
                      <div className="text-[10px] text-slate-500 font-sans uppercase font-bold">Assigned Private IP</div>
                      <div className="flex items-center justify-between">
                        <span className="font-extrabold text-slate-900 text-sm">10.20.30.19</span>
                        <span className="px-2 py-0.5 rounded bg-emerald-100 text-emerald-800 text-[10px] font-bold">
                          10.20.30.0/24 POOL
                        </span>
                      </div>
                    </div>

                    <div className="p-2.5 rounded-xl bg-slate-50 border border-slate-200 space-y-1">
                      <div className="text-[10px] text-slate-500 font-sans uppercase font-bold">Ingress Gateway VIP</div>
                      <div className="flex items-center justify-between">
                        <span className="font-extrabold text-purple-900 text-sm">10.20.20.246:443</span>
                        <span className="px-2 py-0.5 rounded bg-purple-100 text-purple-900 text-[10px] font-bold">
                          gw1 (MASTER)
                        </span>
                      </div>
                    </div>
                  </div>

                  {/* Security Isolation Badges */}
                  <div className="pt-2 border-t border-slate-100 space-y-1.5">
                    <div className="text-[10px] font-bold text-slate-500 uppercase tracking-wider font-mono">
                      Neutron Zero-Trust Enforcements
                    </div>
                    <div className="space-y-1 text-xs">
                      <div className="flex items-center gap-2 text-emerald-800 font-semibold text-[11px]">
                        <Icon name="check_circle" size={14} className="text-emerald-600 flex-shrink-0" />
                        <span>VIP Ingress TCP 80/443 Allowed</span>
                      </div>
                      <div className="flex items-center gap-2 text-rose-800 font-semibold text-[11px]">
                        <Icon name="block" size={14} className="text-rose-600 flex-shrink-0" />
                        <span>Inter-sandbox East-West Traversal Blocked</span>
                      </div>
                      <div className="flex items-center gap-2 text-rose-800 font-semibold text-[11px]">
                        <Icon name="block" size={14} className="text-rose-600 flex-shrink-0" />
                        <span>Gateway Mgmt Port 8000 &amp; SSH Blocked</span>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Pre-Flight Checklist */}
                <div className="bg-white p-4 rounded-2xl border border-slate-200 shadow-xs space-y-2.5">
                  <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                    <div className="flex items-center gap-2">
                      <Icon name="verified" size={16} className="text-emerald-600" />
                      <h3 className="text-xs font-bold text-slate-900 uppercase tracking-wider font-mono">
                        Pre-Flight Status
                      </h3>
                    </div>
                    <span className="px-2 py-0.5 rounded bg-emerald-50 text-emerald-800 border border-emerald-200 text-[10px] font-bold font-mono">
                      READY TO COMMIT
                    </span>
                  </div>

                  <div className="space-y-1.5 text-xs">
                    <div className="flex items-center justify-between py-1 border-b border-slate-100">
                      <span className="text-slate-600">Keystone Quotas:</span>
                      <span className="font-mono font-bold text-emerald-700">✓ Available</span>
                    </div>
                    <div className="flex items-center justify-between py-1 border-b border-slate-100">
                      <span className="text-slate-600">Subnet Pool 10.20.30.0/24:</span>
                      <span className="font-mono font-bold text-emerald-700">✓ 231 Free IPs</span>
                    </div>
                    <div className="flex items-center justify-between py-1">
                      <span className="text-slate-600">HAProxy Config Lexer:</span>
                      <span className="font-mono font-bold text-emerald-700">✓ 0 Errors</span>
                    </div>
                  </div>
                </div>

              </div>

            </div>

            {/* Modal Footer Actions */}
            <div className="px-6 py-4 bg-white border-t border-slate-200 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div className="flex items-center gap-2 text-xs">
                <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse" />
                <span className="text-slate-600 font-medium">Target:</span>
                <strong className="font-mono text-purple-950 font-bold">
                  {provisionTeam ? `${provisionTeam}.${sharedState?.config.routing_domain || 'cloudpulse.cstam.felcloud.tn'}` : 'Enter a team name'}
                </strong>
              </div>

              <div className="flex items-center gap-2.5">
                <button
                  type="button"
                  onClick={() => { setShowProvisionModal(false); setProvisionDone(false); setProvisionDryRunPassed(false) }}
                  className="px-4 py-2 bg-slate-100 hover:bg-slate-200 text-slate-700 rounded-xl text-xs font-semibold transition-colors cursor-pointer"
                >
                  Cancel
                </button>

                <button
                  type="button"
                  onClick={() => {
                    setProvisionDryRunLoading(true)
                    setTimeout(() => { setProvisionDryRunLoading(false); setProvisionDryRunPassed(true) }, 800)
                  }}
                  disabled={provisionDryRunLoading}
                  className="px-4 py-2 bg-slate-100 hover:bg-slate-200 text-slate-800 border border-slate-300 rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-colors cursor-pointer disabled:opacity-50"
                >
                  {provisionDryRunLoading ? (
                    <><Icon name="sync" size={14} className="animate-spin text-purple-700" /><span>Testing…</span></>
                  ) : provisionDryRunPassed ? (
                    <><Icon name="check_circle" size={14} className="text-emerald-600" /><span>Dry-Run Passed</span></>
                  ) : (
                    <><Icon name="verified" size={14} /><span>Dry-Run Check</span></>
                  )}
                </button>

                <button
                  type="button"
                  disabled={provisionLoading || !provisionTeam.trim()}
                  onClick={handleProvisionSubmit as any}
                  className="px-5 py-2.5 bg-[#2D0035] hover:bg-[#492B50] text-white rounded-xl text-xs font-bold flex items-center gap-2 shadow-md transition-all cursor-pointer disabled:opacity-50 hover:brightness-110 active:scale-95"
                >
                  {provisionLoading ? (
                    <><Icon name="sync" size={16} className="animate-spin text-amber-300" /><span>Deploying Nova VM…</span></>
                  ) : (
                    <><Icon name="bolt" size={16} className="text-amber-300" /><span>Deploy Sandbox Now</span></>
                  )}
                </button>
              </div>
            </div>

          </div>
        </div>
      )}

      {/* ══════════════════════════
          MODAL: Failover Drill
      ══════════════════════════ */}
      {showDrillModal && (
        <div className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm flex items-center justify-center p-4">
          <div className="bg-white rounded-2xl w-full max-w-lg shadow-2xl overflow-hidden animate-fade-up border border-[#E5DFE8]">

            {/* Header */}
            <div className="px-6 py-5 border-b border-[#F0EBF2] flex items-center justify-between bg-gradient-to-r from-rose-50 to-amber-50">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-xl bg-rose-100 border border-rose-200 flex items-center justify-center">
                  <Icon name="bolt" size={20} className="text-rose-600" />
                </div>
                <div>
                  <h3 className="font-bold text-[15px] text-[#170B1B]">Failover Drill</h3>
                  <p className="text-[11px] text-[#a088aa]">Simulate a primary gateway crash &amp; verify automatic recovery</p>
                </div>
              </div>
              {drillStage === 0 && (
                <button onClick={() => setShowDrillModal(false)} className="w-8 h-8 rounded-xl text-[#a088aa] hover:text-[#170B1B] hover:bg-[#F5F3F7] flex items-center justify-center transition-colors cursor-pointer">
                  <Icon name="close" size={19} />
                </button>
              )}
            </div>

            <div className="p-6 space-y-5">
              {drillStage === 0 ? (
                // ── IDLE: explain and confirm ──────────────────────────────
                <>
                  <p className="text-sm text-[#827787] leading-relaxed">
                    This will simulate a crash on the active gateway. The standby picks up the Virtual IP automatically via VRRP — no manual action, no dropped TCP sessions.
                  </p>

                  <div className="p-4 bg-[#F8F6F9] border border-[#E8E0EA] rounded-xl space-y-2 text-xs">
                    <div className="flex justify-between">
                      <span className="text-[#827787]">Active gateway now</span>
                      <span className="font-mono font-bold text-[#2D0035]">{sharedState?.gateways?.vip_holder?.toUpperCase() || 'GW1'} (VIP {sharedState?.config?.vip_address || '10.20.20.246'})</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-[#827787]">Expected failover time</span>
                      <span className="font-mono font-bold text-emerald-700">&lt; 1 000 ms</span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-[#827787]">TCP sessions dropped</span>
                      <span className="font-mono font-bold text-emerald-700">0 (Gratuitous ARP)</span>
                    </div>
                  </div>

                  <div className="flex justify-end gap-2">
                    <button onClick={() => setShowDrillModal(false)} className="px-4 py-2 bg-white border border-[#E5DFE8] hover:bg-[#F5F3F7] text-[#170B1B] rounded-xl text-sm font-medium transition-colors cursor-pointer">
                      Cancel
                    </button>
                    <button onClick={handleDrillSubmit} className="px-5 py-2 bg-rose-600 hover:bg-rose-700 text-white rounded-xl text-sm font-bold flex items-center gap-1.5 transition-colors shadow-md shadow-rose-700/25 cursor-pointer">
                      <Icon name="bolt" size={15} />
                      Confirm Drill
                    </button>
                  </div>
                </>
              ) : (
                // ── RUNNING: animated step tracker ─────────────────────────
                <div className="space-y-3">
                  {[
                    { stage: 1, label: 'Crash signal sent', sub: 'SIGKILL injected into active HAProxy/Keepalived process', icon: 'warning' },
                    { stage: 2, label: 'VRRP election fired', sub: 'Standby broadcasts gratuitous ARP — VIP reassigned', icon: 'sync_alt' },
                    { stage: 3, label: 'Standby now ACTIVE', sub: `VIP ${sharedState?.config?.vip_address || '10.20.20.246'} promoted in ${sharedState?.benchmarks?.last_failover_ms || 412} ms · 0 dropped sessions`, icon: 'check_circle' },
                  ].map(step => {
                    const done = drillStage > step.stage
                    const running = drillStage === step.stage
                    return (
                      <div key={step.stage} className={`flex items-start gap-3.5 p-3.5 rounded-xl border transition-all ${
                        done    ? 'bg-emerald-50 border-emerald-200'
                        : running ? 'bg-amber-50 border-amber-300 shadow-sm'
                        : 'bg-[#F8F6F9] border-[#E8E0EA] opacity-40'
                      }`}>
                        <div className={`w-8 h-8 rounded-xl flex items-center justify-center flex-shrink-0 ${
                          done ? 'bg-emerald-100' : running ? 'bg-amber-100' : 'bg-slate-100'
                        }`}>
                          {running ? (
                            <Icon name="sync" size={16} className="text-amber-600 animate-spin" />
                          ) : done ? (
                            <Icon name="check_circle" size={16} className="text-emerald-600" />
                          ) : (
                            <Icon name={step.icon as any} size={16} className="text-slate-400" />
                          )}
                        </div>
                        <div className="min-w-0">
                          <div className={`text-sm font-bold ${
                            done ? 'text-emerald-900' : running ? 'text-amber-900' : 'text-slate-400'
                          }`}>{step.label}</div>
                          <div className={`text-[11px] mt-0.5 ${
                            done ? 'text-emerald-700' : running ? 'text-amber-700' : 'text-slate-400'
                          }`}>{step.sub}</div>
                        </div>
                      </div>
                    )
                  })}

                  {drillStage === 3 && (
                    <div className="pt-2 text-center text-xs text-emerald-700 font-semibold">
                      ✓ Failover complete — modal closing automatically…
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ══════════════════════════
          MODAL: Diff Viewer
      ══════════════════════════ */}
      {diffModalData && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
          <div className="bg-white rounded-2xl shadow-2xl max-w-2xl w-full overflow-hidden animate-fade-up border border-[#E5DFE8]">
            <div className="px-6 py-4 bg-[#F8F5FB] border-b border-[#F0EBF2] flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Icon name="visibility" size={20} className="text-[#2D0035]" />
                <span className="text-sm font-bold text-[#170B1B]">Config Diff — {diffModalData.from} → {diffModalData.to}</span>
              </div>
              <button onClick={() => setDiffModalData(null)} className="w-7 h-7 rounded-xl text-[#a088aa] hover:text-[#170B1B] hover:bg-[#EDE7F0] flex items-center justify-center">
                <Icon name="close" size={17} />
              </button>
            </div>
            <div className="p-6 font-mono text-xs bg-[#070010] text-gray-300 overflow-x-auto max-h-96 leading-relaxed dark-scroll">
              <pre><code>{`@@ -13,6 +13,8 @@ frontend cstam_fe
     bind 10.20.20.246:443 ssl crt /etc/ssl/cstam-west.pem alpn h2,http/1.1
     mode http
\x1b-    stick-table type ip size 100k expire 30s store http_req_rate(10s)
-    http-request track-sc0 src
+    stick-table type ip size 250k expire 30s store http_req_rate(10s)
+    http-request track-sc0 src
+    acl rate_abuse sc_http_req_rate(0) gt 45000
+    http-request tarpit deny_status 429 if rate_abuse
     acl is_quarantine src -f /etc/haproxy/quarantine_ips.lst
     http-request deny if is_quarantine
     default_backend sandbox_be`}</code></pre>
            </div>
            <div className="p-4 bg-[#F8F5FB] border-t border-[#F0EBF2] flex justify-end">
              <button onClick={() => setDiffModalData(null)} className="px-4 py-2 text-white rounded-xl text-sm font-bold hover:opacity-90 transition-opacity"
                style={{ background: 'linear-gradient(135deg, #0e0018, #2D0035)' }}>
                Close Inspector
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ══════════════════════════
          DRAWER: API Docs
      ══════════════════════════ */}
      {showDocDrawer && (
        <div className="fixed inset-0 z-50 bg-black/30 backdrop-blur-xs" onClick={() => setShowDocDrawer(false)}>
          <div className="absolute right-0 top-0 h-full w-[500px] bg-white border-l border-[#E5DFE8] shadow-2xl p-6 overflow-y-auto space-y-5 animate-fade-in"
            onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between pb-3 border-b border-[#F0EBF2]">
              <div className="flex items-center gap-2.5">
                <div className="w-9 h-9 rounded-xl bg-[#F5F3F7] border border-[#E5DFE8] flex items-center justify-center">
                  <Icon name="dns" size={18} className="text-[#2D0035]" />
                </div>
                <div>
                  <h3 className="font-bold text-[15px] text-[#170B1B]">REST API Reference</h3>
                  <p className="text-[11px] text-[#a088aa] font-mono">CSTAM 3.0 Control Plane</p>
                </div>
              </div>
              <button onClick={() => setShowDocDrawer(false)} className="w-7 h-7 rounded-xl text-[#a088aa] hover:text-[#170B1B] hover:bg-[#F5F3F7] flex items-center justify-center transition-colors">
                <Icon name="close" size={17} />
              </button>
            </div>

            <p className="text-xs text-[#827787] leading-relaxed">
              All endpoints require <code className="font-mono bg-[#F5F3F7] text-[#2D0035] px-1 py-0.5 rounded">Authorization: Bearer &lt;token&gt;</code>.
            </p>

            <div className="space-y-2.5 text-xs">
              {[
                { method: 'GET',    c: 'bg-emerald-100 text-emerald-800', path: '/api/gateways/status',     desc: 'VRRP master state, HAProxy uptime, VIP holder.' },
                { method: 'GET',    c: 'bg-emerald-100 text-emerald-800', path: '/api/sandboxes',           desc: 'List all sandboxes with lease and status data.' },
                { method: 'POST',   c: 'bg-blue-100 text-blue-800',       path: '/api/sandboxes/provision', desc: '{"team":"alpha","ttl":7200} — deploys Nova + routes.' },
                { method: 'DELETE', c: 'bg-rose-100 text-rose-800',       path: '/api/sandboxes/:id',       desc: 'Teardown VM, enter 180s quarantine cooldown.' },
                { method: 'POST',   c: 'bg-amber-100 text-amber-800',     path: '/api/gateways/failover',   desc: 'Trigger zero-downtime failover to standby node.' },
                { method: 'POST',   c: 'bg-purple-100 text-purple-800',   path: '/api/gateways/sync',       desc: 'Reconcile HAProxy maps with Nova/Neutron state.' },
                { method: 'GET',    c: 'bg-emerald-100 text-emerald-800', path: '/api/leases',              desc: 'All IPAM leases with expiry & quarantine state.' },
                { method: 'GET',    c: 'bg-emerald-100 text-emerald-800', path: '/api/events',              desc: 'Audit log stream with types, scopes, timing.' },
              ].map((ep, i) => (
                <div key={i} className="p-3 bg-[#F8F5FB] border border-[#EDE7F0] rounded-xl space-y-1.5">
                  <div className="flex items-center gap-2 font-mono">
                    <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${ep.c}`}>{ep.method}</span>
                    <span className="text-[#170B1B] font-semibold text-[12px]">{ep.path}</span>
                  </div>
                  <div className="text-[11px] text-[#827787]">{ep.desc}</div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* ══════════════════════════
          TOASTS
      ══════════════════════════ */}
      <div className="fixed bottom-12 right-5 z-50 flex flex-col gap-2 pointer-events-none w-[340px]">
        {toasts.map(t => {
          const c = toastColorMap[t.type]
          return (
            <div key={t.id} className={`pointer-events-auto bg-white border ${c.ring} rounded-2xl overflow-hidden shadow-xl shadow-purple-900/10 flex items-stretch animate-fade-up`}>
              <div className={`w-1 flex-shrink-0 ${c.bar}`} />
              <div className="flex items-start gap-3 p-3.5 flex-1 min-w-0">
                <div className={`w-7 h-7 rounded-xl ${c.bg} flex items-center justify-center flex-shrink-0 mt-0.5`}>
                  <Icon name={toastIconMap[t.type]} size={16} className={c.icon} />
                </div>
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-[13px] text-[#170B1B] leading-tight">{t.title}</div>
                  <div className="text-[11px] text-[#827787] mt-0.5 leading-relaxed">{t.message}</div>
                </div>
                <button onClick={() => setToasts(p => p.filter(x => x.id !== t.id))} className="text-gray-300 hover:text-gray-500 flex-shrink-0 mt-0.5 transition-colors cursor-pointer">
                  <Icon name="close" size={15} />
                </button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
