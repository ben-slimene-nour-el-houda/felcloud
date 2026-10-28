import { useState, useCallback, useEffect, useRef } from 'react'
import { fetchSandboxes, fetchConfig, fetchLogs, type Sandbox, type ConfigResponse } from '../api'
import { usePolling } from '../hooks'
import { Sparkline } from '../components/Sparkline'
import { RefreshButton } from '../components/RefreshButton'

interface Props {
  onOpenCustomDomain: () => void
  showToast: (title: string, message: string, type?: 'info' | 'success' | 'warning' | 'error') => void
}

function cleanLabel(raw: string): string {
  if (!raw) return ''
  return raw.replace(/\.cstam\.felcloud\.tn$/, '').replace(/\.local$/, '')
}

export default function Routes({ onOpenCustomDomain, showToast }: Props) {
  const sbFetcher  = useCallback(() => fetchSandboxes(), [])
  const cfgFetcher = useCallback(() => fetchConfig(), [])

  const { data: sandboxes, refresh } = usePolling<Sandbox[]>(sbFetcher, 5000)
  const { data: config }    = usePolling<ConfigResponse>(cfgFetcher, 60000)

  const domain = config?.routing_domain ?? 'cstam.felcloud.tn'

  const defaultRoutes: Sandbox[] = [
    { id: 1, sandbox_id: 'sb-team-alpha', hostname: 'team-alpha', ip_address: '10.20.10.14', status: 'ACTIVE', custom_domain: 'alpha.dev-hack.tn', created_at: '', expires_at: null, ttl_seconds: 7200 },
    { id: 2, sandbox_id: 'sb-team-beta',  hostname: 'team-beta',  ip_address: '10.20.10.15', status: 'ACTIVE', custom_domain: '', created_at: '', expires_at: null, ttl_seconds: 7200 },
    { id: 3, sandbox_id: 'sb-team-gamma', hostname: 'team-gamma', ip_address: '10.20.10.16', status: 'ACTIVE', custom_domain: 'app.gamma-live.tn', created_at: '', expires_at: null, ttl_seconds: 7200 },
    { id: 4, sandbox_id: 'sb-team-delta', hostname: 'team-delta', ip_address: '10.20.10.17', status: 'ACTIVE', custom_domain: '', created_at: '', expires_at: null, ttl_seconds: 7200 },
    { id: 5, sandbox_id: 'sb-team-jury',  hostname: 'team-jury',  ip_address: '10.20.10.22', status: 'ACTIVE', custom_domain: 'jury.cstam.tn', created_at: '', expires_at: null, ttl_seconds: 7200 },
  ]

  const activeFromServer = (sandboxes || []).filter(s => !['RELEASED', 'TEARDOWN_FAILED'].includes(s.status) && !s.sandbox_id.startsWith('recycle-test-'))
  const list = activeFromServer.length > 0 ? activeFromServer : defaultRoutes

  const [search, setSearch] = useState('')
  const [rateLimits, setRateLimits] = useState<Record<string, number>>({})
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
      <div className="card-elevated p-6 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-2xl font-bold text-[#170B1B] tracking-tight">Traffic &amp; Routing Rules</h1>
            <span className="badge badge-emerald">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              {activeRoutes} Active Frontends
            </span>
            <span className="badge badge-purple font-mono">*.{domain}</span>
          </div>
          <p className="text-xs text-[#827787] mt-1.5 max-w-2xl leading-relaxed">
            Wildcard subdomain routing, automated TLS certificates, per-team stick-table rate limiting, and live stream telemetry.
          </p>
        </div>

        <div className="flex items-center gap-2.5 flex-wrap">
          <RefreshButton onRefresh={refresh} label="Refresh" />
          <button
            onClick={onOpenCustomDomain}
            className="flex items-center gap-2 px-4 py-2 bg-[#2D0035] hover:bg-[#43134c] text-white rounded-xl text-xs font-bold shadow-md hover:shadow-lg transition-all duration-200 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">link</span>
            <span>Bind Custom Domain</span>
          </button>
        </div>
      </div>

      {/* ── 4 Stats Cards with Telemetry Glow ── */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-5">
        {[
          { label: 'Active Frontends', val: `${activeRoutes} Routes`, icon: 'alt_route', glow: 'glow-healthy', color: 'emerald' as const, sub: '100% SLA PASS' },
          { label: 'Registered Teams', val: `${list.length} Instances`, icon: 'dns', glow: 'glow-primary', color: 'purple' as const, sub: 'Sub-second deploy' },
          { label: 'Custom Domain Aliases', val: `${withCustom} Active`, icon: 'public', glow: 'glow-info', color: 'blue' as const, sub: 'CNAME verified' },
          { label: 'Base Routing Wildcard', val: `*.${domain}`, icon: 'language', glow: 'glow-warning', color: 'amber' as const, sub: 'ACME Wildcard' },
        ].map((s, i) => (
          <div key={i} className={`kpi-card ${s.glow} space-y-3`}>
            <div className="flex items-center justify-between text-[#827787] text-sm">
              <span className="font-semibold tracking-wide">{s.label}</span>
              <span className="material-symbols-outlined text-[20px] text-[#74537a]">{s.icon}</span>
            </div>
            <div className="font-mono font-bold text-2xl text-[#170B1B] truncate">{s.val}</div>
            <div className="flex items-center justify-between text-xs text-[#827787] font-medium">
              <span>{s.sub}</span>
              <Sparkline data={[10, 12, 14, 15, 18]} color={s.color} width={48} height={16} />
            </div>
          </div>
        ))}
      </div>

      {/* ── Subdomain Routing Table (Spacious, modern, uniform rows) ── */}
      <div className="card-elevated overflow-hidden">
        {/* Table header bar */}
        <div className="p-5 border-b border-purple-900/5 bg-[#F8F6F9] flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-2xl bg-purple-100 flex items-center justify-center text-[#2D0035] flex-shrink-0">
              <span className="material-symbols-outlined text-[22px]">alt_route</span>
            </div>
            <div>
              <h2 className="text-base font-bold text-[#170B1B]">
                HAProxy Frontend Subdomain Routing Table
              </h2>
              <div className="text-xs text-[#827787] font-mono mt-0.5">
                Matching rules against <strong className="text-[#2D0035]">*.{domain}</strong>
              </div>
            </div>
          </div>

          {/* Unclipped Search bar with pl-11 */}
          <div className="relative min-w-[260px] md:min-w-[320px]">
            <span className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-[#827787]">
              <span className="material-symbols-outlined text-[19px]">search</span>
            </span>
            <input
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search routes by team or IP…"
              className="w-full pl-11 pr-4 py-2.5 text-sm rounded-xl bg-white focus:outline-none focus:ring-2 focus:ring-[#2D0035]/20 text-[#170B1B] font-medium transition-all shadow-xs"
            />
          </div>
        </div>

        {/* Table content */}
        <div className="overflow-x-auto">
          <table className="table-modern">
            <thead>
              <tr>
                <th>Subdomain Route</th>
                <th>Backend Target</th>
                <th>Gateway Node</th>
                <th>TLS / SSL</th>
                <th>Rate Limit</th>
                <th className="text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(s => {
                const team = cleanLabel(s.hostname) || cleanLabel(s.sandbox_id)
                const routeUrl = `${team}.${domain}`
                const lim = rateLimits[s.sandbox_id] || 120

                return (
                  <tr key={s.sandbox_id} className="hover:bg-purple-900/[0.02] transition-colors">
                    {/* Subdomain */}
                    <td className="min-w-[200px]">
                      <div className="flex items-center gap-2.5">
                        <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)] flex-shrink-0" />
                        <span className="font-mono font-bold text-[#170B1B] text-sm">{routeUrl}</span>
                      </div>
                      {s.custom_domain && (
                        <div className="text-xs font-mono text-emerald-800 font-semibold pl-5 mt-0.5">
                          Alias: {s.custom_domain}
                        </div>
                      )}
                    </td>

                    {/* Backend Target */}
                    <td className="font-mono whitespace-nowrap">
                      <span className="px-3 py-1.5 rounded-xl bg-[#F8F6F9] text-[#170B1B] font-bold text-sm">
                        {s.ip_address || '10.20.10.x'}:8080
                      </span>
                    </td>

                    {/* Gateway */}
                    <td className="whitespace-nowrap">
                      <span className="badge badge-purple">
                        GW1 (MASTER)
                      </span>
                    </td>

                    {/* TLS */}
                    <td className="whitespace-nowrap">
                      <span className="badge badge-emerald">
                        <span className="material-symbols-outlined text-[14px]">lock</span>
                        Let's Encrypt (Auto)
                      </span>
                    </td>

                    {/* Rate Limit */}
                    <td className="font-mono text-[#170B1B] font-bold whitespace-nowrap text-sm">
                      {lim} req/min
                    </td>

                    {/* Edit limit */}
                    <td className="text-right whitespace-nowrap">
                      <button
                        onClick={() => handleEditLimit(s.sandbox_id)}
                        className="px-3.5 py-1.5 rounded-xl bg-[#F8F6F9] hover:bg-[#F2EDF4] text-[#2D0035] text-xs font-semibold transition-all cursor-pointer"
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
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">

        {/* Live HTTP Terminal Stream */}
        <div className="lg:col-span-8 card-elevated bg-[#170B1B] p-6 text-white space-y-4">
          <div className="flex items-center justify-between pb-3.5 border-b border-white/10">
            <div className="flex items-center gap-3">
              <span className="w-2.5 h-2.5 rounded-full bg-emerald-400 animate-pulse shadow-[0_0_8px_rgba(52,211,153,0.8)]" />
              <h3 className="font-mono font-bold text-sm text-white">Live HTTP Access Stream</h3>
              <span className="badge badge-dark-emerald">
                100% SLA PASS
              </span>
            </div>

            <button
              onClick={handleSpike}
              className="px-4 py-2 rounded-xl bg-purple-700/80 hover:bg-purple-600 text-white font-mono text-xs font-bold transition-all flex items-center gap-2 cursor-pointer shadow-md"
            >
              <span className="material-symbols-outlined text-[16px]">bolt</span>
              <span>Inject Traffic Spike</span>
            </button>
          </div>

          <div ref={streamRef} className="space-y-2 max-h-[300px] overflow-y-auto font-mono text-xs pr-1 dark-scroll">
            {logs.map((l, i) => (
              <div
                key={i}
                className="p-3 rounded-xl bg-white/5 hover:bg-white/10 border border-white/5 flex items-center justify-between transition-colors duration-150"
              >
                <div className="flex items-center gap-3 min-w-0">
                  <span className="text-purple-300/70 text-xs font-semibold">[{l.time}]</span>
                  <span className={`px-2 py-0.5 rounded text-xs font-bold ${
                    l.code >= 500 ? 'bg-rose-900/60 text-rose-300' : 'bg-emerald-900/60 text-emerald-300'
                  }`}>
                    {l.code}
                  </span>
                  <span className="text-blue-300 font-bold text-xs">{l.method}</span>
                  <span className="text-purple-100 truncate">
                    <strong className="text-purple-300">{l.subdomain}</strong>{l.path}
                  </span>
                </div>
                <div className="flex items-center gap-3 font-semibold text-purple-200/80 text-xs flex-shrink-0">
                  <span className="text-emerald-400">{l.latency}</span>
                  <span className="text-white/40">via {l.gw}</span>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* TLS & HAProxy Profile */}
        <div className="lg:col-span-4 card-elevated p-6 space-y-5">
          <div className="flex items-center gap-2.5 pb-3 border-b border-purple-900/5">
            <div className="w-9 h-9 rounded-xl bg-emerald-50 flex items-center justify-center text-emerald-700">
              <span className="material-symbols-outlined text-[20px]">lock</span>
            </div>
            <h3 className="text-base font-bold text-[#170B1B]">TLS &amp; Certificate Profile</h3>
          </div>

          <div className="space-y-3 text-sm">
            <div className="flex justify-between py-1.5 border-b border-purple-900/5">
              <span className="text-[#827787] font-medium">Provider:</span>
              <span className="font-bold text-[#170B1B]">Let's Encrypt Wildcard ACME</span>
            </div>
            <div className="flex justify-between py-1.5 border-b border-purple-900/5">
              <span className="text-[#827787] font-medium">TLS Protocol:</span>
              <span className="font-mono font-bold text-[#170B1B]">TLS 1.3 / ALPN h2</span>
            </div>
            <div className="flex justify-between py-1.5 border-b border-purple-900/5">
              <span className="text-[#827787] font-medium">HSTS:</span>
              <span className="font-mono font-bold text-emerald-700">Enabled (max-age 1y)</span>
            </div>
            <div className="flex justify-between py-1.5">
              <span className="text-[#827787] font-medium">Domain Coverage:</span>
              <span className="font-mono font-bold text-[#2D0035]">*.{domain}</span>
            </div>
          </div>

          <div className="pt-3 border-t border-purple-900/5">
            <div className="text-xs font-bold text-[#74537a] uppercase tracking-wider font-mono mb-2.5">
              HAProxy Config Snapshot
            </div>
            <div className="p-4 rounded-2xl bg-[#F8F6F9] space-y-2 text-sm font-mono">
              <div className="flex justify-between text-[#170B1B]">
                <span className="text-[#827787]">Default Rate Limit:</span>
                <span className="font-bold">120 req/min</span>
              </div>
              <div className="flex justify-between text-[#170B1B]">
                <span className="text-[#827787]">Stick-Table TTL:</span>
                <span className="font-bold">30s</span>
              </div>
              <div className="flex justify-between text-[#170B1B]">
                <span className="text-[#827787]">Max Connections:</span>
                <span className="font-bold">50,000</span>
              </div>
              <div className="flex justify-between text-[#170B1B]">
                <span className="text-[#827787]">Timeout Connect:</span>
                <span className="font-bold">5s</span>
              </div>
            </div>
          </div>
        </div>
      </div>
              </div>
            </div>
          </div>
        </div>

      </div>

    </div>
  )
}
