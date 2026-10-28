import { useState, useCallback, useEffect, useRef } from 'react'
import { fetchLogs, type LogsResponse } from '../api'
import { RefreshButton } from '../components/RefreshButton'

export default function TrafficLogs() {
  const [gateway, setGateway] = useState('gw1')
  const [source, setSource] = useState('haproxy')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [logs, setLogs] = useState<LogsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  const doFetch = useCallback(async () => {
    try {
      const data = await fetchLogs(gateway, source, 100)
      setLogs(data)
      setError(null)
    } catch (e: any) {
      setError(e.message || 'Failed to fetch logs')
    }
  }, [gateway, source])

  useEffect(() => {
    doFetch()
    if (!autoRefresh) return
    const id = setInterval(doFetch, 3000)
    return () => clearInterval(id)
  }, [doFetch, autoRefresh])

  useEffect(() => {
    if (autoRefresh && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [logs, autoRefresh])

  function getLineColor(line: string): string {
    if (/ 5\d{2} /.test(line)) return '#f43f5e'
    if (/ 4\d{2} /.test(line)) return '#f59e0b'
    if (/ [23]\d{2} /.test(line)) return '#10b981'
    return '#c4b5fd'
  }

  return (
    <div className="space-y-6">
      {/* ── Page Header ── */}
      <div className="card-elevated p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-3xl font-bold text-[#170B1B] tracking-tight">Traffic &amp; Daemon Logs</h1>
            <span className="badge badge-purple font-mono">{gateway.toUpperCase()}</span>
            <span className="badge badge-emerald">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              Live Ingress
            </span>
          </div>
          <p className="text-sm text-[#827787] mt-1.5 leading-relaxed max-w-2xl">
            Real-time streaming syslog output from HAProxy frontend routers and Keepalived VRRP daemons across cluster nodes.
          </p>
        </div>

        <div className="flex items-center gap-4 flex-wrap">
          <label className="flex items-center gap-2 text-sm font-medium text-[#827787] cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={e => setAutoRefresh(e.target.checked)}
              className="w-4 h-4 rounded accent-[#2D0035]"
            />
            <span>Auto-refresh (3s)</span>
          </label>

          <RefreshButton onRefresh={doFetch} label="Refresh" />
        </div>
      </div>

      {/* Controls Card */}
      <div className="card-elevated p-5 flex flex-wrap items-center justify-between gap-4">
        <div className="flex flex-wrap items-center gap-4">
          {/* Node selector */}
          <div className="flex items-center gap-1.5 p-1.5 rounded-2xl bg-[#F8F6F9]">
            {['gw1', 'gw2'].map(gw => (
              <button
                key={gw}
                onClick={() => setGateway(gw)}
                className={`px-5 py-2 rounded-xl text-sm font-bold font-mono transition-all duration-200 cursor-pointer ${
                  gateway === gw
                    ? 'bg-[#2D0035] text-white shadow-xs'
                    : 'text-[#827787] hover:text-[#170B1B]'
                }`}
              >
                {gw.toUpperCase()}
              </button>
            ))}
          </div>

          {/* Source selector */}
          <div className="flex items-center gap-1.5 p-1.5 rounded-2xl bg-[#F8F6F9]">
            {['haproxy', 'keepalived'].map(s => (
              <button
                key={s}
                onClick={() => setSource(s)}
                className={`px-5 py-2 rounded-xl text-sm font-semibold transition-all duration-200 cursor-pointer ${
                  source === s
                    ? 'bg-[#2D0035] text-white shadow-xs'
                    : 'text-[#827787] hover:text-[#170B1B]'
                }`}
              >
                {s.charAt(0).toUpperCase() + s.slice(1)}
              </button>
            ))}
          </div>
        </div>

        <span className="text-xs text-[#827787] font-mono">{logs?.count ?? 0} log records streamed</span>
      </div>

      {error && (
        <div className="p-4 rounded-2xl text-xs text-rose-700 bg-rose-50 border border-rose-200">{error}</div>
      )}

      {/* Log viewer */}
      <div className="card-elevated overflow-hidden bg-[#170B1B] text-white">
        <div className="flex items-center justify-between px-6 py-4 border-b border-white/10 bg-white/5">
          <div className="flex items-center gap-3">
            <h3 className="text-sm font-bold text-white font-mono">{gateway.toUpperCase()} — {source}</h3>
            {autoRefresh && (
              <span className="badge badge-dark-emerald">
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                LIVE STREAM
              </span>
            )}
          </div>
          <span className="text-xs text-purple-200/60 font-mono">{logs?.count ?? 0} lines buffered</span>
        </div>
        <div className="overflow-y-auto max-h-[calc(100vh-320px)] p-6 font-mono text-xs leading-relaxed dark-scroll">
          {(logs?.entries ?? []).map((line, i) => (
            <div key={i} className="py-1 hover:bg-white/5 px-2 rounded-lg transition-colors flex items-start gap-4" style={{ color: getLineColor(line) }}>
              <span className="text-white/30 select-none inline-block w-8 text-right shrink-0">{i + 1}</span>
              <span className="break-all">{line}</span>
            </div>
          ))}
          {(!logs?.entries || logs.entries.length === 0) && (
            <p className="text-white/40 text-center py-12">No log entries found on node</p>
          )}
          <div ref={bottomRef} />
        </div>
      </div>
    </div>
  )
}
