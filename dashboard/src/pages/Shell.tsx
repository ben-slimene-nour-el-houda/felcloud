import { useState, useCallback } from 'react'
import { forgetToken, fetchConfig, type ConfigResponse } from '../api'
import { usePolling } from '../hooks'
import Overview from './Overview'
import Sandboxes from './Sandboxes'
import Gateways from './Gateways'
import Routes from './Routes'
import TrafficLogs from './TrafficLogs'
import Events from './Events'
import Leases from './Leases'

const NAV = [
  { id: 'overview',  label: 'Overview',    icon: '⊞' },
  { id: 'sandboxes', label: 'Sandboxes',   icon: '◧' },
  { id: 'gateways',  label: 'Gateways',    icon: '⇄' },
  { id: 'routes',    label: 'Routes',      icon: '◉' },
  { id: 'logs',      label: 'Traffic Logs', icon: '≡' },
  { id: 'events',    label: 'Events',      icon: '⏱' },
  { id: 'leases',    label: 'Leases',      icon: '⊡' },
] as const

type Page = typeof NAV[number]['id']

export default function Shell({ onLogout }: { onLogout: () => void }) {
  const [page, setPage] = useState<Page>('overview')
  const [collapsed, setCollapsed] = useState(false)

  const configFetcher = useCallback(() => fetchConfig(), [])
  const { data: config } = usePolling<ConfigResponse>(configFetcher, 60000)

  function handleLogout() {
    forgetToken()
    onLogout()
  }

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Sidebar */}
      <aside
        className="flex flex-col border-r transition-all duration-300 shrink-0"
        style={{
          width: collapsed ? 64 : 240,
          background: 'var(--color-bg-secondary)',
          borderColor: 'var(--color-border)',
        }}
      >
        {/* Logo */}
        <div className="flex items-center gap-3 px-4 h-16 border-b" style={{ borderColor: 'var(--color-border)' }}>
          <div className="shrink-0 w-8 h-8 rounded-lg flex items-center justify-center" style={{ background: 'linear-gradient(135deg, #6366f1, #a855f7)' }}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
            </svg>
          </div>
          {!collapsed && <span className="font-bold text-white tracking-tight">FelCloud</span>}
        </div>

        {/* Nav items */}
        <nav className="flex-1 py-3 px-2 space-y-1 overflow-y-auto">
          {NAV.map((item) => (
            <button
              key={item.id}
              id={`nav-${item.id}`}
              onClick={() => setPage(item.id)}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-all duration-200 ${
                page === item.id
                  ? 'text-white'
                  : 'text-[var(--color-text-secondary)] hover:text-white hover:bg-white/5'
              }`}
              style={page === item.id ? { background: 'linear-gradient(135deg, rgba(99,102,241,0.2), rgba(168,85,247,0.15))' } : {}}
            >
              <span className="text-lg w-6 text-center shrink-0">{item.icon}</span>
              {!collapsed && item.label}
              {page === item.id && !collapsed && (
                <span className="ml-auto w-1.5 h-1.5 rounded-full bg-[var(--color-accent)]" />
              )}
            </button>
          ))}
        </nav>

        {/* Bottom */}
        <div className="p-3 border-t space-y-2" style={{ borderColor: 'var(--color-border)' }}>
          <button
            onClick={() => setCollapsed(!collapsed)}
            className="w-full flex items-center justify-center gap-2 px-3 py-2 rounded-lg text-xs text-[var(--color-text-muted)] hover:text-white hover:bg-white/5 transition-colors"
          >
            {collapsed ? '→' : '← Collapse'}
          </button>
          <button
            id="logout-btn"
            onClick={handleLogout}
            className="w-full flex items-center justify-center gap-2 px-3 py-2 rounded-lg text-xs text-red-400 hover:text-red-300 hover:bg-red-400/10 transition-colors"
          >
            {collapsed ? '⏻' : '⏻ Sign Out'}
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-y-auto">
        {/* Top bar */}
        <header className="sticky top-0 z-10 flex items-center justify-between h-16 px-6 border-b" style={{ background: 'rgba(10, 14, 26, 0.85)', backdropFilter: 'blur(12px)', borderColor: 'var(--color-border)' }}>
          <h2 className="text-lg font-semibold text-white">
            {NAV.find(n => n.id === page)?.label}
          </h2>
          <div className="flex items-center gap-4 text-xs text-[var(--color-text-muted)]">
            {config && (
              <>
                <span className="px-2 py-1 rounded-md" style={{ background: 'var(--color-bg-card)' }}>
                  {config.routing_domain}
                </span>
                <span className="px-2 py-1 rounded-md" style={{ background: config.provisioner === 'mock' ? 'var(--color-warning-muted)' : 'var(--color-success-muted)' }}>
                  {config.provisioner === 'mock' ? '🧪 Mock' : '☁ OpenStack'}
                </span>
                <span>v{config.version}</span>
              </>
            )}
          </div>
        </header>

        {/* Page content */}
        <div className="p-6 animate-fade-in" key={page}>
          {page === 'overview' && <Overview />}
          {page === 'sandboxes' && <Sandboxes />}
          {page === 'gateways' && <Gateways />}
          {page === 'routes' && <Routes />}
          {page === 'logs' && <TrafficLogs />}
          {page === 'events' && <Events />}
          {page === 'leases' && <Leases />}
        </div>
      </main>
    </div>
  )
}
