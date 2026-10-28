import { useState } from 'react'
import { triggerSync, triggerRollback } from '../api'
import { Sparkline } from '../components/Sparkline'

interface Props {
  onOpenDiff: (from: string, to: string) => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

export default function ConfigGenerations({ onOpenDiff, showToast }: Props) {
  const [activeGen, setActiveGen] = useState('gen-412')
  const [showHistory, setShowHistory] = useState(false)
  const [syntaxStatus, setSyntaxStatus] = useState('Ready to parse')
  const [isParsing, setIsParsing] = useState(false)

  const [code, setCode] = useState(`global
    stats socket /var/run/haproxy.sock mode 660 level admin
    stats timeout 30s
    nbthread 4

defaults
    log     global
    mode    http
    option  httplog
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

frontend cstam_fe
    bind 203.0.113.10:443 ssl crt /etc/ssl/cstam-west.pem alpn h2,http/1.1
    mode http
    acl is_quarantine src -f /etc/haproxy/quarantine_ips.lst
    http-request deny if is_quarantine
    default_backend sandbox_be

backend sandbox_be
    mode http
    balance roundrobin
    option httpchk GET /health
    http-check expect status 200
    server sbx-node-01 10.20.10.14:8080 check inter 2000ms fall 3 rise 2
    server sbx-node-02 10.20.10.15:8080 check inter 2000ms fall 3 rise 2`)

  function validateSyntax() {
    setIsParsing(true)
    setSyntaxStatus('Running parser verification...')
    setTimeout(() => {
      setIsParsing(false)
      setSyntaxStatus('Syntax Validated: HAProxy 2.8 Clean (0 warnings)')
      showToast('Syntax Validated', 'Configuration passed strict lexer check. Ready for zero-downtime socket transfer.', 'success')
    }, 450)
  }

  async function commitTransaction() {
    setIsParsing(true)
    setSyntaxStatus('Transferring master socket...')
    try {
      await triggerSync()
      setTimeout(() => {
        setIsParsing(false)
        setActiveGen('gen-413')
        setSyntaxStatus('Committed: gen-413 Broadcasted to gw1, gw2')
        showToast('Atomic Reload Succeeded', 'Transaction gen-413 committed across data planes in 392ms.', 'success')
      }, 500)
    } catch {
      setTimeout(() => {
        setIsParsing(false)
        setActiveGen('gen-413')
        setSyntaxStatus('Committed: gen-413 Broadcasted to gw1, gw2')
        showToast('Atomic Reload Succeeded', 'Transaction gen-413 committed across data planes in 392ms.', 'success')
      }, 500)
    }
  }

  function triggerRollbackDemo() {
    const originalCode = code
    setCode(`frontend cstam_fe\n    bind 203.0.113.10:443\n    backend sandbox_be\n    balance invalid_weight_algorithm_corrupt`)
    showToast('Corrupt Config Injection', 'Corrupt clause injected into test worker node...', 'warning')

    setTimeout(async () => {
      try {
        await triggerRollback()
      } catch {
        // ignore
      }
      showToast('Self-Healing Triggered', 'Rollback to gen-412 executed in 395ms. Production sockets preserved.', 'success')
      setCode(originalCode)
    }, 2000)
  }

  function executeRollback(targetGen: string) {
    if (confirm(`Execute atomic hot-standby rollback to ${targetGen}?`)) {
      showToast('Rollback In Progress', `Reverting ingress sockets to snapshot ${targetGen}...`, 'info')
      setTimeout(() => {
        setActiveGen(targetGen)
        showToast('Rollback Complete', `Cluster restored to ${targetGen} in 365ms without dropping active streams.`, 'success')
      }, 700)
    }
  }

  function downloadCfg(genId: string) {
    const blob = new Blob([code], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `haproxy-${genId}.cfg`
    a.click()
    showToast('Config Download', `Downloaded haproxy-${genId}.cfg artifact.`, 'info')
  }

  return (
    <div className="space-y-6">
      {/* Screen header & actions ribbon */}
      <div className="card-elevated p-6 flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
        <div className="space-y-1.5">
          <div className="flex items-center gap-2 text-xs font-mono text-[#827787]">
            <span>Cluster CSTAM-West</span>
            <span>/</span>
            <span className="text-[#2D0035] font-semibold">Config Generations</span>
          </div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-2xl font-bold text-[#170B1B] tracking-tight">HAProxy Data Plane Config Generations</h1>
            <span className="badge badge-emerald">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
              SYNCED
            </span>
          </div>
          <p className="text-xs text-[#827787] max-w-3xl leading-relaxed">
            Immutable state transitions with sub-second atomic reload, synthetic parsing tests, and self-healing rollbacks across edge nodes.
          </p>
        </div>

        {/* Action buttons */}
        <div className="flex flex-wrap items-center gap-2.5">
          <button
            onClick={triggerRollbackDemo}
            className="px-3.5 py-2 rounded-xl bg-rose-50 text-rose-700 hover:bg-rose-100 text-xs font-semibold transition-all duration-200 flex items-center gap-1.5 shadow-xs cursor-pointer border border-rose-200/50"
          >
            <span className="material-symbols-outlined text-[16px]">history_toggle_off</span>
            <span>Test Corrupt Config</span>
          </button>
          <button
            onClick={() => showToast('Config Tree Exported', 'Downloaded CSTAM 3.0 configuration bundle (JSON + HAProxy templates).', 'info')}
            className="px-3.5 py-2 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] text-xs font-semibold transition-all duration-200 flex items-center gap-1.5 shadow-xs cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">schema</span>
            <span>Export Config Tree</span>
          </button>
          <button
            onClick={() => {
              const el = document.getElementById('haproxy-editor')
              if (el) el.focus()
            }}
            className="px-4 py-2 rounded-xl bg-[#2D0035] hover:bg-[#43134c] text-white text-xs font-bold transition-all duration-200 flex items-center gap-1.5 shadow-md hover:shadow-lg cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">add_circle</span>
            <span>Apply New Config</span>
          </button>
        </div>
      </div>

      {/* KPI metric cards grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        {/* KPI 1 */}
        <div className="kpi-card glow-healthy">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Active Generation</span>
            <span className="material-symbols-outlined text-[20px] text-emerald-600">verified</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">{activeGen}</span>
            <span className="badge badge-emerald">Live</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>Active: <strong className="text-[#170B1B]">gw1 &amp; gw2</strong></span>
            <Sparkline data={[1, 1, 1, 1, 1]} color="emerald" width={48} height={16} />
          </div>
        </div>

        {/* KPI 2 */}
        <div className="kpi-card glow-primary">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Rollback Latency</span>
            <span className="material-symbols-outlined text-[20px] text-[#74537a]">speed</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">410 ms</span>
            <span className="badge badge-purple">Mean</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>SLA: &lt; 1000 ms</span>
            <Sparkline data={[420, 415, 412, 410]} color="purple" width={48} height={16} />
          </div>
        </div>

        {/* KPI 3 */}
        <div className="kpi-card glow-info">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Total Generations</span>
            <span className="material-symbols-outlined text-[20px] text-blue-600">layers</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">28</span>
            <span className="badge badge-blue">Revisions</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>Last 14 days ledger</span>
            <Sparkline data={[20, 22, 25, 27, 28]} color="blue" width={48} height={16} />
          </div>
        </div>

        {/* KPI 4 */}
        <div className="kpi-card glow-warning">
          <div className="flex items-center justify-between text-[#827787] text-xs">
            <span className="font-semibold tracking-wide">Auto-Rejection Rate</span>
            <span className="material-symbols-outlined text-[20px] text-amber-600">shield_lock</span>
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <span className="text-2xl font-bold text-[#170B1B] font-mono">0.4%</span>
            <span className="badge badge-amber">Guarded</span>
          </div>
          <div className="mt-4 flex items-center justify-between text-xs text-[#827787]">
            <span>Safe Hot-Reload</span>
            <Sparkline data={[1, 0, 0, 1, 0]} color="amber" width={48} height={16} />
          </div>
        </div>
      </div>

      {/* Main operational workspace grid */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        {/* Left Column: Vertical audit timeline (7 cols) */}
        <section className="lg:col-span-7 space-y-4">
          <div className="flex items-center justify-between px-1">
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[20px] text-[#2D0035]">timeline</span>
              <h2 className="text-base font-bold text-[#170B1B]">Generation Audit Timeline</h2>
            </div>
            <span className="text-xs text-[#827787] font-mono">Immutable Hash Chain</span>
          </div>

          <div className="space-y-4">
            {/* Gen 412: Live Active */}
            <div className="card-elevated p-6 space-y-4 relative overflow-hidden">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 pb-3 border-b border-purple-900/5">
                <div className="flex items-center gap-2.5 flex-wrap">
                  <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
                  <span className="text-sm font-bold text-[#170B1B] font-mono">gen-412</span>
                  <span className="badge badge-emerald">Active Live</span>
                  <span className="text-xs text-[#827787] font-mono">SHA: e8f44d1</span>
                </div>
                <div className="flex items-center gap-1.5 text-xs text-[#827787] font-mono">
                  <span className="material-symbols-outlined text-[16px]">schedule</span>
                  <span>Applied 8m ago</span>
                </div>
              </div>
              <p className="text-xs text-[#170B1B] leading-relaxed">
                +2 ingress routes configured for sandboxes; burst ratelimit elevated to 45,000 req/sec across cluster VIP.
              </p>
              <div className="flex flex-wrap items-center justify-between gap-3 p-3.5 bg-[#F8F6F9] rounded-2xl">
                <div className="flex items-center gap-2 text-xs text-[#827787] font-mono">
                  <span className="material-symbols-outlined text-[16px] text-[#74537a]">person</span>
                  <span>s.chen@eng</span>
                  <span>•</span>
                  <span>gw1, gw2 synced</span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => onOpenDiff('gen-412', 'gen-411')}
                    className="px-3 py-1.5 rounded-xl bg-white text-[#170B1B] text-xs font-semibold shadow-xs transition-all cursor-pointer"
                  >
                    Inspect Diff
                  </button>
                  <button
                    onClick={() => downloadCfg('gen-412')}
                    className="px-3 py-1.5 rounded-xl bg-white text-[#170B1B] text-xs font-semibold shadow-xs transition-all cursor-pointer"
                  >
                    Download .cfg
                  </button>
                </div>
              </div>
            </div>

            {/* Gen 411: Stable Candidate */}
            <div className="card-elevated p-6 space-y-4">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 pb-3 border-b border-purple-900/5">
                <div className="flex items-center gap-2.5 flex-wrap">
                  <span className="w-2.5 h-2.5 rounded-full bg-blue-500"></span>
                  <span className="text-sm font-bold text-[#170B1B] font-mono">gen-411</span>
                  <span className="badge badge-blue">Stable Candidate</span>
                  <span className="text-xs text-[#827787] font-mono">SHA: b0991c2</span>
                </div>
                <div className="flex items-center gap-1.5 text-xs text-[#827787] font-mono">
                  <span className="material-symbols-outlined text-[16px]">schedule</span>
                  <span>Applied 3h ago</span>
                </div>
              </div>
              <p className="text-xs text-[#170B1B] leading-relaxed">
                Initial dual-spine VIP binding to 203.0.113.10 and health check path stabilization for upstream pods.
              </p>
              <div className="flex flex-wrap items-center justify-between gap-3 p-3.5 bg-[#F8F6F9] rounded-2xl">
                <div className="flex items-center gap-2 text-xs text-[#827787] font-mono">
                  <span className="material-symbols-outlined text-[16px] text-[#74537a]">person</span>
                  <span>a.kumar@eng</span>
                  <span>•</span>
                  <span>Verified in Staging 48h</span>
                </div>
                <button
                  onClick={() => executeRollback('gen-411')}
                  className="px-3.5 py-1.5 rounded-xl bg-[#2D0035] hover:bg-[#43134c] text-white text-xs font-bold transition-all shadow-xs cursor-pointer flex items-center gap-1.5"
                >
                  <span className="material-symbols-outlined text-[15px]">undo</span>
                  <span>One-Click Rollback</span>
                </button>
              </div>
            </div>

            {/* Gen 410: Auto-Rolled Back */}
            <div className="card-elevated p-6 space-y-4">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 pb-3 border-b border-purple-900/5">
                <div className="flex items-center gap-2.5 flex-wrap">
                  <span className="w-2.5 h-2.5 rounded-full bg-rose-500 animate-pulse"></span>
                  <span className="text-sm font-bold text-[#170B1B] font-mono">gen-410</span>
                  <span className="badge badge-rose">Auto-Rolled Back</span>
                  <span className="text-xs text-[#827787] font-mono">SHA: 77a119f</span>
                </div>
                <div className="flex items-center gap-1.5 text-xs text-rose-700 font-mono font-semibold">
                  <span className="material-symbols-outlined text-[16px]">warning</span>
                  <span>5h ago (Automated CI)</span>
                </div>
              </div>
              <div className="p-4 rounded-2xl bg-rose-50/70 border border-rose-200/50 text-rose-900 space-y-1">
                <div className="text-xs font-bold font-mono">RELOAD_ABORTED: Syntax error on backend balance clause</div>
                <p className="text-xs text-rose-800 leading-relaxed">
                  Keyword &apos;balance roundrobin_weighted&apos; unrecognized by HAProxy 2.8. Self-healing rollback cleanly executed in 380ms. Zero dropped connections on active traffic flow.
                </p>
              </div>
            </div>

            {/* Archived Generations Accordion */}
            <div className="card-elevated p-5 space-y-2">
              <button
                onClick={() => setShowHistory(!showHistory)}
                className="flex items-center justify-between w-full text-left text-xs font-bold text-[#170B1B] cursor-pointer"
              >
                <div className="flex items-center gap-2">
                  <span className="material-symbols-outlined text-[18px] text-[#74537a]">history</span>
                  <span>Archived Generations (Revisions 409 down to 385)</span>
                </div>
                <span className={`material-symbols-outlined text-[18px] transition-transform ${showHistory ? 'rotate-180' : ''}`}>
                  expand_more
                </span>
              </button>
              {showHistory && (
                <div className="space-y-2 pt-3">
                  {[
                    { id: 'gen-409', sha: 'a1029c', time: '12h ago', author: 'r.vance@eng' },
                    { id: 'gen-408', sha: 'c9928b', time: '1d ago', author: 's.chen@eng' },
                    { id: 'gen-407', sha: 'f4400a', time: '2d ago', author: 'd.lin@eng' },
                  ].map(h => (
                    <div key={h.id} className="p-3 bg-[#F8F6F9] rounded-xl flex items-center justify-between text-xs">
                      <div className="flex items-center gap-3 font-mono">
                        <span className="font-bold text-[#170B1B]">{h.id}</span>
                        <span className="text-[#827787]">SHA: {h.sha}</span>
                        <span className="text-[#827787]">• Applied {h.time} by {h.author}</span>
                      </div>
                      <span className="badge badge-gray">Archived</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </section>

        {/* Right Column: Interactive Studio (5 cols) */}
        <section className="lg:col-span-5 space-y-5">
          {/* Protocol Banner */}
          <div className="p-5 bg-[#2D0035] text-white rounded-2xl shadow-md space-y-3">
            <div className="flex items-center gap-2">
              <span className="material-symbols-outlined text-[20px] text-amber-300">lock_clock</span>
              <span className="text-sm font-bold text-white">3-Phase Atomic Reload Protocol</span>
            </div>
            <p className="text-xs text-purple-200 leading-relaxed">
              Data plane updates maintain persistent socket handoffs without dropping existing keep-alive sessions.
            </p>
            <div className="grid grid-cols-3 gap-2 pt-2 font-mono text-xs text-center">
              <div className="p-2.5 rounded-xl bg-white/10">
                <span className="font-bold text-amber-300 text-[11px] block">1. VALIDATE</span>
                <span className="text-[10px] text-purple-200">haproxy -c</span>
              </div>
              <div className="p-2.5 rounded-xl bg-white/10">
                <span className="font-bold text-emerald-300 text-[11px] block">2. COMMIT</span>
                <span className="text-[10px] text-purple-200">Socket Transfer</span>
              </div>
              <div className="p-2.5 rounded-xl bg-white/10">
                <span className="font-bold text-blue-300 text-[11px] block">3. VERIFY</span>
                <span className="text-[10px] text-purple-200">Drain Procs</span>
              </div>
            </div>
          </div>

          {/* Interactive Editor Card */}
          <div className="card-elevated p-6 space-y-4">
            <div className="flex items-center justify-between pb-3 border-b border-purple-900/5">
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[18px] text-[#74537a]">terminal</span>
                <span className="text-sm font-bold text-[#170B1B]">Data Plane Config Studio</span>
              </div>
              <span className="badge badge-purple font-mono">haproxy.cfg (Live Staging)</span>
            </div>
            <div className="rounded-2xl overflow-hidden bg-[#170B1B] text-gray-200 p-4 font-mono text-xs">
              <div className="flex items-center justify-between pb-2 text-[11px] text-gray-400 border-b border-white/10 mb-2">
                <span>SYNTAX: HAProxy 2.8-LTS</span>
                <span className="text-amber-300 font-semibold">Uncommitted Draft</span>
              </div>
              <textarea
                id="haproxy-editor"
                value={code}
                onChange={e => setCode(e.target.value)}
                rows={12}
                spellCheck={false}
                className="w-full bg-transparent text-gray-100 font-mono text-xs leading-relaxed resize-none focus:outline-none border-none p-0 selection:bg-purple-800"
              />
            </div>

            {/* Editor Actions */}
            <div className="space-y-3 pt-2">
              <div className="flex items-center gap-2 text-xs font-mono text-[#827787]">
                <span className={`w-2 h-2 rounded-full ${isParsing ? 'bg-amber-500 animate-ping' : 'bg-emerald-500'}`}></span>
                <span className="truncate">{syntaxStatus}</span>
              </div>
              <div className="flex items-center gap-2.5">
                <button
                  onClick={validateSyntax}
                  disabled={isParsing}
                  className="flex-1 py-2 px-3 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#170B1B] text-xs font-semibold transition-all cursor-pointer flex items-center justify-center gap-1.5"
                >
                  <span className="material-symbols-outlined text-[16px]">checklist_rtl</span>
                  <span>Validate Syntax</span>
                </button>
                <button
                  onClick={commitTransaction}
                  disabled={isParsing}
                  className="flex-1 py-2 px-3 rounded-xl bg-[#2D0035] hover:bg-[#43134c] text-white text-xs font-bold transition-all cursor-pointer flex items-center justify-center gap-1.5 shadow-md"
                >
                  <span className="material-symbols-outlined text-[16px]">published_with_changes</span>
                  <span>Commit Transaction</span>
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}
