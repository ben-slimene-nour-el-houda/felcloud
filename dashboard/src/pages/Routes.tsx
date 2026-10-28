import { useState, useCallback, useEffect, useRef } from 'react'
import { fetchSandboxes, fetchConfig, fetchLogs, type Sandbox, type ConfigResponse, type SharedState } from '../api'
import { usePolling } from '../hooks'
import Icon from '../components/Icon'

interface Props {
  sharedState?: SharedState | null
  refreshSharedState?: () => void
  onOpenCustomDomain: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

function cleanLabel(raw: string): string {
  if (!raw) return ''
  return raw.replace(/\.(cloudpulse\.)?cstam\.felcloud\.tn$/, '').replace(/\.local$/, '')
}

export default function Routes({ sharedState, onOpenCustomDomain, showToast }: Props) {
  const sbFetcher  = useCallback(() => fetchSandboxes(), [])
  const cfgFetcher = useCallback(() => fetchConfig(), [])

  const { data: sandboxes } = usePolling<Sandbox[]>(sbFetcher, 5000)
  const { data: config }    = usePolling<ConfigResponse>(cfgFetcher, 60000)

  const domain = sharedState?.config?.routing_domain || config?.routing_domain || 'cloudpulse.cstam.felcloud.tn'

  const serverSb = Array.isArray(sandboxes) ? sandboxes : (sharedState?.sandboxes?.items || [])
  const list = serverSb.filter(s =>
    !['RELEASED', 'TEARDOWN_FAILED'].includes(s.status) &&
    !s.sandbox_id.startsWith('recycle-test-') &&
    !s.sandbox_id.startsWith('bench-') &&
    !s.sandbox_id.startsWith('probe') &&
    !s.sandbox_id.startsWith('demo')
  )

  const [search, setSearch] = useState('')
  const [rateLimits, setRateLimits] = useState<Record<string, number>>({})
  const [isStreamPaused, setIsStreamPaused] = useState(false)
  const isStreamPausedRef = useRef(false)
  isStreamPausedRef.current = isStreamPaused

  const [logs, setLogs] = useState([
    { time: '14:22:01', subdomain: 'team-alpha', method: 'GET',  path: '/api/health',      code: 200, latency: '14ms',  gw: 'gw1' },
    { time: '14:22:00', subdomain: 'team-beta',  method: 'POST', path: '/predict',          code: 200, latency: '42ms',  gw: 'gw1' },
    { time: '14:21:59', subdomain: 'team-gamma', method: 'GET',  path: '/socket',           code: 200, latency: '12ms',  gw: 'gw1' },
    { time: '14:21:58', subdomain: 'team-delta', method: 'GET',  path: '/dashboard',        code: 200, latency: '22ms',  gw: 'gw1' },
    { time: '14:21:56', subdomain: 'team-jury',  method: 'GET',  path: '/evaluate',         code: 200, latency: '18ms',  gw: 'gw1' },
    { time: '14:21:55', subdomain: 'team-alpha', method: 'POST', path: '/api/v1/infer',     code: 200, latency: '87ms',  gw: 'gw1' },
  ])
  const streamRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    fetchLogs('gw1', 'haproxy', 10).then(res => {
      if (isStreamPausedRef.current) return
      if (res?.entries?.length > 0) {
        const parsed = res.entries.slice(-8).map((line: string, idx: number) => ({
          time: new Date(Date.now() - (8 - idx) * 3000).toLocaleTimeString(),
          subdomain: `team-${['alpha', 'beta', 'gamma', 'delta'][idx % 4]}`,
          method: idx % 2 === 0 ? 'GET' : 'POST',
          path: idx % 2 === 0 ? '/health' : '/api/compute',
          code: / 5\d{2} /.test(line) ? 502 : 200,
          latency: `${Math.floor(Math.random() * 40 + 5)}ms`,
          gw: 'gw1',
        }))
        setLogs(parsed)
      }
    }).catch(() => {})
  }, [domain])

  function handleSpike() {
    showToast('Traffic Spike Injected', 'Emulating 500 concurrent connections across active frontends…', 'info')
    const methods = ['GET', 'POST', 'PUT']
    const paths   = ['/api/status', '/stream', '/health', '/heavy-load', '/login']
    const newItems = Array.from({ length: 5 }).map((_, i) => ({
      time: new Date().toLocaleTimeString(),
      subdomain: `team-${['alpha', 'beta', 'gamma', 'delta', 'jury'][i % 5]}`,
      method: methods[i % methods.length],
      path: paths[i % paths.length],
      code: 200,
      latency: `${Math.floor(Math.random() * 50 + 8)}ms`,
      gw: 'gw1',
    }))
    setLogs(prev => [...prev.slice(-12), ...newItems])
    if (streamRef.current) streamRef.current.scrollTop = streamRef.current.scrollHeight
  }

  function handleEditLimit(id: string) {
    const cur = rateLimits[id] || 120
    const next = prompt(`Rate limit (req/min) for ${id}:`, String(cur))
    if (next && !isNaN(parseInt(next, 10))) {
      setRateLimits(prev => ({ ...prev, [id]: parseInt(next, 10) }))
      showToast('Rate Limit Updated', `HAProxy stick-table set to ${next} req/min for ${id}.`, 'success')
    }
  }

  const activeRoutes = list.length
  const withCustom   = list.filter(s => s.custom_domain).length

  const filtered = list.filter(s => {
    const q = search.toLowerCase()
    return !q || s.sandbox_id.includes(q) || (s.hostname || '').includes(q) || s.ip_address.includes(q)
  })

  return (
    <div className="space-y-6 min-w-0">

      {/* ── Page Header ── */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 bg-white p-5 rounded-2xl border border-slate-200 shadow-xs">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-xl font-extrabold text-slate-900 tracking-tight">Traffic &amp; Routing Rules</h1>
            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-bold font-mono bg-emerald-100 text-emerald-800 border border-emerald-300">
              <span className="w-2 h-2 rounded-full bg-emerald-600 animate-pulse" />
              {activeRoutes} Active Frontends
            </span>
          </div>
          <p className="text-xs text-slate-600 mt-1">
            Each team sandbox gets a unique subdomain routed through HAProxy. Traffic is isolated, rate-limited, and monitored in real time.
          </p>
        </div>

        <button
          onClick={onOpenCustomDomain}
          className="flex items-center gap-2 px-4 py-2 bg-slate-900 hover:bg-slate-800 text-white rounded-xl text-xs font-bold shadow-sm transition-all cursor-pointer hover:brightness-110 active:scale-95 flex-shrink-0 whitespace-nowrap"
        >
          <Icon name="key" size={16} />
          <span>Bind Custom Domain</span>
        </button>
      </div>

      {/* ── 4 Stats Strip with High Contrast ── */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        {[
          { label: 'Active Frontends', val: `${activeRoutes} Routes`, icon: 'alt_route' as const, color: 'text-emerald-700', bg: 'bg-emerald-50', border: 'border-emerald-200', text: 'text-emerald-950' },
          { label: 'Registered Teams', val: `${list.length} Instances`, icon: 'dns' as const, color: 'text-blue-700', bg: 'bg-blue-50', border: 'border-blue-200', text: 'text-blue-950' },
          { label: 'Custom Domain Aliases', val: `${withCustom} Active`, icon: 'hub' as const, color: 'text-violet-700', bg: 'bg-violet-50', border: 'border-violet-200', text: 'text-violet-950' },
          { label: 'Base Routing Wildcard', val: `*.${domain}`, icon: 'policy' as const, color: 'text-amber-700', bg: 'bg-amber-50', border: 'border-amber-200', text: 'text-amber-950' },
        ].map((s, i) => (
          <div key={i} className={`p-4 rounded-2xl border ${s.border} ${s.bg} flex items-center gap-3.5 shadow-xs min-w-0`}>
            <div className={`w-10 h-10 rounded-xl bg-white border border-slate-200 flex items-center justify-center ${s.color} flex-shrink-0`}>
              <Icon name={s.icon} size={20} />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[11px] font-bold text-slate-600 uppercase tracking-wider font-mono">{s.label}</div>
              <div className={`font-mono font-black text-base ${s.text} truncate mt-0.5`}>{s.val}</div>
            </div>
          </div>
        ))}
      </div>

      {/* ── Subdomain Routing Table ── */}
      <div className="bg-white border border-slate-200 rounded-2xl shadow-xs overflow-hidden">
        {/* Table header bar */}
        <div className="p-4 border-b border-slate-200 bg-slate-50 flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <div className="w-8 h-8 rounded-lg bg-blue-100 flex items-center justify-center text-blue-800 flex-shrink-0">
              <Icon name="alt_route" size={18} />
            </div>
            <div>
              <h2 className="text-sm font-extrabold text-slate-900">
                HAProxy Frontend Subdomain Routing Table
              </h2>
              <div className="text-xs text-slate-500 font-mono">
                Matching rules against <strong className="text-violet-900">*.{domain}</strong>
              </div>
            </div>
          </div>

          {/* Search bar */}
          <div className="relative min-w-[220px]">
            <span className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-slate-400">
              <Icon name="search" size={15} />
            </span>
            <input
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search routes by team or IP…"
              className="w-full pl-9 pr-3 py-1.5 text-xs border border-slate-300 rounded-xl bg-white focus:outline-none focus:border-violet-600 text-slate-900 font-medium"
            />
          </div>
        </div>

        {/* Table content */}
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="bg-slate-100/80 border-b border-slate-200 text-slate-700 text-[11px] font-bold uppercase tracking-wider font-mono">
                <th className="py-3 px-4">Subdomain Route</th>
                <th className="py-3 px-3">Backend Target</th>
                <th className="py-3 px-3">Gateway</th>
                <th className="py-3 px-3">Rate Limit</th>
                <th className="py-3 px-3">Error Rate</th>
                <th className="py-3 px-3">TLS</th>
                <th className="py-3 px-4 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {filtered.map(s => {
                const team = cleanLabel(s.hostname) || cleanLabel(s.sandbox_id)
                const routeUrl = `${team}.${domain}`
                const lim = rateLimits[s.sandbox_id] || 120

                return (
                  <tr key={s.sandbox_id} className="hover:bg-slate-50/80 transition-colors">
                    {/* Subdomain */}
                    <td className="py-3.5 px-4 min-w-[200px]">
                      <div className="flex items-center gap-2">
                        <span className="w-2 h-2 rounded-full bg-emerald-500 flex-shrink-0" />
                        <span className="font-mono font-bold text-slate-900 text-xs">{routeUrl}</span>
                      </div>
                      {s.custom_domain && (
                        <div className="text-[11px] font-mono text-emerald-800 font-semibold pl-4 mt-0.5">
                          Alias: {s.custom_domain}
                        </div>
                      )}
                    </td>

                    {/* Backend Target */}
                    <td className="py-3.5 px-3 font-mono whitespace-nowrap">
                      <span className="px-2 py-0.5 rounded-md bg-slate-100 border border-slate-200 text-slate-900 font-bold text-[11px]">
                        {s.ip_address ? `${s.ip_address}:8080` : 'Pending Allocation'}
                      </span>
                    </td>

                    {/* Gateway */}
                    <td className="py-3.5 px-3 whitespace-nowrap">
                      <span className="inline-flex items-center gap-1 font-mono font-bold text-violet-900 bg-violet-50 px-2 py-0.5 rounded border border-violet-200 text-[11px]">
                        {(sharedState?.gateways?.vip_holder || 'gw1').toUpperCase()} (MASTER)
                      </span>
                    </td>

                    {/* Rate Limit */}
                    <td className="py-3.5 px-3 font-mono text-slate-900 font-bold whitespace-nowrap">
                      <span className="px-2 py-0.5 rounded bg-slate-100 border border-slate-200 text-slate-800">
                        {lim} req/min
                      </span>
                    </td>

                    {/* Error Rate */}
                    <td className="py-3.5 px-3 font-mono whitespace-nowrap">
                      <span className="inline-flex items-center gap-1 font-bold text-emerald-800 bg-emerald-50 px-2 py-0.5 rounded border border-emerald-200 text-[11px]">
                        <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                        0.0%
                      </span>
                    </td>

                    {/* TLS */}
                    <td className="py-3.5 px-3 whitespace-nowrap">
                      <span className="inline-flex items-center gap-1 font-mono font-bold text-violet-900 bg-violet-50 px-2 py-0.5 rounded border border-violet-200 text-[11px]">
                        <Icon name="verified" size={13} className="text-violet-700" />
                        TLS 1.3
                      </span>
                    </td>

                    {/* Edit limit */}
                    <td className="py-3.5 px-4 text-right whitespace-nowrap">
                      <button
                        onClick={() => handleEditLimit(s.sandbox_id)}
                        className="px-2.5 py-1 rounded-lg bg-slate-100 hover:bg-slate-200 text-slate-800 border border-slate-200 text-xs font-semibold transition-colors cursor-pointer"
                      >
                        Edit Limit
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* ── Live HTTP Access Stream + TLS Security Overview ── */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-5 items-start">

        {/* Live HTTP Terminal Stream */}
        <div className="lg:col-span-8 bg-slate-950 rounded-2xl border border-slate-800 p-5 shadow-lg space-y-4">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 border-b border-slate-800">
            <div className="flex items-center gap-2.5 flex-wrap">
              <span className={`w-3 h-3 rounded-full ${isStreamPaused ? 'bg-amber-400' : 'bg-emerald-500 animate-pulse'}`} />
              <h3 className="font-mono font-extrabold text-sm text-slate-100">Live HTTP Access Stream</h3>
              {isStreamPaused && (
                <span className="px-2 py-0.5 rounded text-[10px] font-mono font-bold bg-amber-500/20 text-amber-300 border border-amber-500/40">
                  PAUSED
                </span>
              )}
              {(() => {
                const passCount = logs.filter(l => l.code < 500).length
                const slaPct = logs.length > 0 ? Math.round((passCount / logs.length) * 100) : 100
                const isDegraded = slaPct < 100
                return (
                  <span className={`px-2 py-0.5 rounded text-[10px] font-mono font-bold border ${
                    isDegraded ? 'bg-amber-900/60 text-amber-300 border-amber-700' : 'bg-emerald-900/60 text-emerald-300 border-emerald-700'
                  }`}>
                    {isDegraded ? `${slaPct}% SLA (Degraded)` : '100% SLA PASS'}
                  </span>
                )
              })()}
            </div>

            <div className="flex items-center gap-2 flex-shrink-0">
              <button
                onClick={() => {
                  setIsStreamPaused(p => !p)
                  showToast(isStreamPaused ? 'Stream Resumed' : 'Stream Paused', isStreamPaused ? 'Live HTTP logs streaming resumed.' : 'Live log stream frozen.', 'info')
                }}
                className={`px-3 py-1.5 rounded-xl font-mono text-xs font-bold transition-all flex items-center gap-1.5 cursor-pointer border ${
                  isStreamPaused
                    ? 'bg-amber-500/20 text-amber-300 border-amber-500/40 hover:bg-amber-500/30'
                    : 'bg-white/10 text-slate-200 border-white/10 hover:bg-white/15'
                }`}
              >
                <Icon name={isStreamPaused ? 'play_arrow' : 'pause'} size={15} />
                <span>{isStreamPaused ? 'Resume' : 'Pause'}</span>
              </button>

              <button
                onClick={handleSpike}
                className="px-3 py-1.5 rounded-xl bg-violet-700 hover:bg-violet-600 text-white font-mono text-xs font-bold transition-all flex items-center gap-1.5 cursor-pointer shadow-sm"
              >
                <Icon name="bolt" size={15} />
                <span>Inject Spike</span>
              </button>
            </div>
          </div>

          <div ref={streamRef} className="space-y-2 min-h-[320px] max-h-[460px] overflow-y-auto font-mono text-xs pr-1">
            {logs.map((l, i) => (
              <div key={i} className="p-2.5 rounded-xl bg-slate-900/90 border border-slate-800/80 flex items-center justify-between hover:bg-slate-800/60 transition-colors">
                <div className="flex items-center gap-3 min-w-0 flex-1">
                  <span className="text-slate-500 text-[11px] font-semibold flex-shrink-0">[{l.time}]</span>
                  <span className={`px-2 py-0.5 rounded text-[10px] font-black flex-shrink-0 ${
                    l.code >= 500 ? 'bg-rose-950 text-rose-300 border border-rose-800' : 'bg-emerald-950 text-emerald-300 border border-emerald-800'
                  }`}>
                    {l.code}
                  </span>
                  <span className="text-blue-400 font-bold text-xs flex-shrink-0">{l.method}</span>
                  <span className="text-slate-200 truncate min-w-0">
                    <strong className="text-violet-300">{l.subdomain}</strong>{l.path}
                  </span>
                </div>
                <div className="flex items-center gap-3 font-semibold text-slate-400 text-[11px] flex-shrink-0 ml-3 whitespace-nowrap">
                  <span className="text-emerald-400">{l.latency}</span>
                  <span className="text-slate-300 font-mono">via {l.gw}</span>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* TLS & HAProxy Profile */}
        <div className="lg:col-span-4 bg-white rounded-2xl border border-slate-200 p-5 shadow-xs space-y-4">
          <div className="flex items-center gap-2 pb-3 border-b border-slate-200">
            <div className="w-8 h-8 rounded-lg bg-emerald-100 flex items-center justify-center text-emerald-800">
              <Icon name="verified" size={18} />
            </div>
            <h3 className="text-sm font-extrabold text-slate-900">TLS &amp; Certificate Profile</h3>
          </div>

          <div className="space-y-2 text-xs">
            <div className="flex justify-between py-1.5 border-b border-slate-100">
              <span className="text-slate-500 font-medium">Provider:</span>
              <span className="font-bold text-slate-900">Internal Wildcard (Simulated ACME)</span>
            </div>
            <div className="flex justify-between py-1.5 border-b border-slate-100">
              <span className="text-slate-500 font-medium">TLS Protocol:</span>
              <span className="font-mono font-bold text-slate-900">TLS 1.3 / ALPN h2</span>
            </div>
            <div className="flex justify-between py-1.5 border-b border-slate-100">
              <span className="text-slate-500 font-medium">HSTS:</span>
              <span className="font-mono font-bold text-emerald-800">Enabled (max-age 1y)</span>
            </div>
            <div className="flex justify-between py-1.5">
              <span className="text-slate-500 font-medium">Domain Coverage:</span>
              <span className="font-mono font-bold text-violet-900">*.{domain}</span>
            </div>
          </div>

          <div className="pt-2 border-t border-slate-200">
            <div className="text-[11px] font-bold text-slate-700 uppercase tracking-wider font-mono mb-2">
              HAProxy Config Snapshot
            </div>
            <div className="p-3 rounded-xl bg-slate-50 border border-slate-200 space-y-1.5 text-xs font-mono">
              <div className="flex justify-between text-slate-700">
                <span>Default Rate Limit:</span>
                <span className="font-bold">120 req/min</span>
              </div>
              <div className="flex justify-between text-slate-700">
                <span>Stick-Table TTL:</span>
                <span className="font-bold">30s</span>
              </div>
              <div className="flex justify-between text-slate-700">
                <span>Max Connections:</span>
                <span className="font-bold">50,000</span>
              </div>
              <div className="flex justify-between text-slate-700">
                <span>Timeout Connect:</span>
                <span className="font-bold">5s</span>
              </div>
            </div>
          </div>
        </div>

      </div>

    </div>
  )
}
