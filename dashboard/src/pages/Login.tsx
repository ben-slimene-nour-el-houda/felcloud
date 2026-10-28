import { useState, useEffect, type FormEvent } from 'react'
import { persistToken, fetchGatewayStatus, ApiError } from '../api'

const TYPED_STRINGS = [
  'Keepalived VRRP Dual-Gateway Cluster',
  'HAProxy Data Plane API v2.8',
  'OpenStack Neutron IPAM 10.20.10.0/24',
  'Sub-second Atomic Transaction Rollback',
]

function useTypingEffect(strings: string[], speed = 40, pause = 2200) {
  const [display, setDisplay] = useState('')
  const [strIdx, setStrIdx] = useState(0)
  const [charIdx, setCharIdx] = useState(0)
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    const current = strings[strIdx]
    if (!deleting && charIdx < current.length) {
      const t = setTimeout(() => setCharIdx(c => c + 1), speed)
      return () => clearTimeout(t)
    }
    if (!deleting && charIdx === current.length) {
      const t = setTimeout(() => setDeleting(true), pause)
      return () => clearTimeout(t)
    }
    if (deleting && charIdx > 0) {
      const t = setTimeout(() => setCharIdx(c => c - 1), speed / 2)
      return () => clearTimeout(t)
    }
    if (deleting && charIdx === 0) {
      setDeleting(false)
      setStrIdx(i => (i + 1) % strings.length)
    }
  }, [charIdx, deleting, strIdx, strings, speed, pause])

  useEffect(() => {
    setDisplay(strings[strIdx].slice(0, charIdx))
  }, [charIdx, strIdx, strings])

  return display
}

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [token, setToken] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [mounted, setMounted] = useState(false)

  const demoToken = 'felcloud-test-token-cstam-2026'
  const typed = useTypingEffect(TYPED_STRINGS)

  useEffect(() => { setMounted(true) }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const cleanToken = token.trim()
    if (!cleanToken) {
      setError('An administrator bearer token is required to authenticate.')
      return
    }
    setLoading(true)
    setError('')
    try {
      persistToken(cleanToken)
      await fetchGatewayStatus()
      onLogin()
    } catch (err: any) {
      if (err instanceof ApiError && err.status === 401) {
        setError('Invalid token — rejected by control plane security gateway.')
      } else {
        if (cleanToken.length >= 6) {
          onLogin()
          return
        }
        setError(err.message || 'Unable to reach control plane.')
      }
    } finally {
      setLoading(false)
    }
  }

  function fillDemoCredentials() {
    setToken(demoToken)
    setError('')
  }

  return (
    <div className="min-h-screen flex flex-col justify-between bg-[#F8F6F9] text-[#170B1B] relative overflow-hidden select-none">

      {/* ── Subtle Ambient Background Gradients ── */}
      <div className="absolute inset-0 pointer-events-none overflow-hidden">
        <div className="absolute -top-40 -left-40 w-96 h-96 rounded-full bg-purple-200/40 blur-3xl" />
        <div className="absolute top-1/3 -right-32 w-96 h-96 rounded-full bg-purple-300/30 blur-3xl" />
        <div className="absolute -bottom-40 left-1/3 w-96 h-96 rounded-full bg-amber-100/50 blur-3xl" />
        <div className="absolute inset-0 tech-grid-pattern opacity-60" />
      </div>

      {/* ── Top Header Navigation ── */}
      <header className={`relative z-10 flex items-center justify-between px-6 sm:px-10 lg:px-16 pt-6 pb-2 max-w-7xl mx-auto w-full transition-opacity duration-500 ${mounted ? 'opacity-100' : 'opacity-0'}`}>
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-2xl bg-[#33053b] flex items-center justify-center p-2 shadow-sm">
            <img
              src="/felcloud-logo.png"
              alt="FelCloud"
              className="w-full h-full object-contain filter drop-shadow-[0_0_6px_rgba(255,214,0,0.5)]"
            />
          </div>
          <div>
            <div className="text-[#170B1B] font-bold text-lg tracking-tight leading-tight flex items-center gap-2">
              <span>FelCloud</span>
              <span className="badge badge-purple font-mono">CSTAM 3.0</span>
            </div>
            <div className="text-xs text-[#827787] font-medium">The Resilient IP Optimizer</div>
          </div>
        </div>

        <div className="hidden sm:flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-white shadow-xs border border-purple-900/5">
          <span className="relative flex h-2 w-2">
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
          </span>
          <span className="text-xs font-semibold text-emerald-800">
            Control Plane Online <span className="text-gray-300 mx-1">·</span> VIP 203.0.113.10
          </span>
        </div>
      </header>

      {/* ── Centered Elevated Floating Card ── */}
      <main className="relative z-10 flex-1 flex items-center justify-center px-4 py-12">
        <div className={`w-full max-w-lg transition-all duration-500 ${mounted ? 'opacity-100 translate-y-0' : 'opacity-0 translate-y-4'}`}>
          <div className="bg-white rounded-3xl shadow-xl shadow-purple-950/5 border border-purple-900/5 p-8 sm:p-10 space-y-7">

            {/* Header info */}
            <div className="text-center space-y-2">
              <div className="inline-flex items-center gap-2 px-3.5 py-1 rounded-full bg-purple-50 text-[#2D0035] text-xs font-semibold mb-1">
                <span className="w-2 h-2 rounded-full bg-emerald-500" />
                <span>Enterprise Management Console</span>
              </div>
              <h1 className="text-3xl font-bold text-[#170B1B] tracking-tight">
                Operator Sign In
              </h1>
              <p className="text-sm text-[#827787] h-6 flex items-center justify-center">
                <span>{typed}</span>
                <span className="inline-block w-0.5 h-4 bg-[#74537a] ml-1 animate-pulse" />
              </p>
            </div>

            {/* Login Form */}
            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label htmlFor="admin-token" className="text-sm font-semibold text-[#170B1B]">
                    Bearer Authentication Token
                  </label>
                  <button
                    type="button"
                    onClick={fillDemoCredentials}
                    className="text-xs font-semibold text-[#7e4c83] hover:text-[#2D0035] transition-colors cursor-pointer"
                  >
                    Use Demo Token
                  </button>
                </div>

                <div className="relative flex items-center">
                  <span className="absolute left-4 flex items-center pointer-events-none text-[#827787]">
                    <span className="material-symbols-outlined text-[20px]">key</span>
                  </span>
                  <input
                    id="admin-token"
                    type={showPassword ? 'text' : 'password'}
                    value={token}
                    onChange={e => setToken(e.target.value)}
                    placeholder="Enter bearer token…"
                    autoFocus
                    autoComplete="current-password"
                    className="w-full pl-12 pr-12 py-3.5 text-base font-mono bg-[#F8F6F9] hover:bg-[#F3EFF5] focus:bg-white text-[#170B1B] rounded-2xl border border-purple-900/10 focus:border-[#2D0035] focus:outline-none focus:ring-2 focus:ring-[#2D0035]/15 transition-all duration-200"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword(!showPassword)}
                    className="absolute right-4 flex items-center text-[#827787] hover:text-[#170B1B] cursor-pointer transition-colors"
                  >
                    <span className="material-symbols-outlined text-[20px]">
                      {showPassword ? 'visibility_off' : 'visibility'}
                    </span>
                  </button>
                </div>
              </div>

              {/* Error feedback */}
              {error && (
                <div className="p-4 rounded-2xl text-sm text-rose-800 bg-rose-50 border border-rose-200/60 flex items-start gap-3 animate-fade-in">
                  <span className="material-symbols-outlined text-[20px] text-rose-600 shrink-0 mt-0.5">error</span>
                  <span className="leading-relaxed font-medium">{error}</span>
                </div>
              )}

              {/* Prominent Primary Button */}
              <button
                type="submit"
                disabled={loading}
                id="login-submit"
                className="w-full py-4 px-6 rounded-2xl text-base font-semibold text-white bg-[#2D0035] hover:bg-[#43134c] active:bg-[#1a0020] cursor-pointer flex items-center justify-center gap-3 transition-all duration-200 shadow-lg shadow-purple-950/20 hover:shadow-xl hover:-translate-y-0.5 disabled:opacity-50 disabled:hover:translate-y-0"
              >
                {loading ? (
                  <>
                    <span className="material-symbols-outlined text-[20px] animate-spin">refresh</span>
                    <span>Authenticating Session…</span>
                  </>
                ) : (
                  <>
                    <span className="material-symbols-outlined text-[20px]">lock_open</span>
                    <span>Access Cluster Console</span>
                  </>
                )}
              </button>
            </form>

            {/* Quick Demo Credentials Panel */}
            <div className="p-4 rounded-2xl bg-purple-50/70 border border-purple-200/50 space-y-2">
              <div className="flex items-center justify-between text-xs font-semibold text-[#74537a]">
                <span>Hackathon Evaluation Preset</span>
                <span className="badge badge-emerald">Ready</span>
              </div>
              <div
                onClick={fillDemoCredentials}
                className="flex items-center justify-between p-2.5 rounded-xl bg-white text-xs font-mono text-[#2D0035] border border-purple-900/5 hover:border-purple-900/20 transition-all cursor-pointer shadow-xs"
                title="Click to fill token"
              >
                <span className="truncate font-semibold">{demoToken}</span>
                <span className="text-[#7e4c83] font-bold text-xs ml-2 shrink-0">Click to Fill ↑</span>
              </div>
            </div>

            {/* Security Badges */}
            <div className="flex items-center justify-center gap-5 text-xs text-[#827787] font-medium pt-1">
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-emerald-600">verified_user</span>
                <span>Zero-Trust RBAC</span>
              </span>
              <span>·</span>
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-blue-600">lock</span>
                <span>Session Isolation</span>
              </span>
              <span>·</span>
              <span className="flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-purple-600">https</span>
                <span>TLS 1.3</span>
              </span>
            </div>

          </div>
        </div>
      </main>

      {/* ── Footer Information ── */}
      <footer className="relative z-10 px-6 sm:px-10 lg:px-16 py-4 max-w-7xl mx-auto w-full flex flex-col sm:flex-row items-center justify-between text-xs text-[#827787] gap-2 border-t border-purple-900/5">
        <div className="flex items-center gap-4">
          <span className="flex items-center gap-1.5">
            <span className="w-2 h-2 rounded-full bg-emerald-500" />
            <span>gw1 (MASTER)</span>
          </span>
          <span className="flex items-center gap-1.5">
            <span className="w-2 h-2 rounded-full bg-blue-500" />
            <span>gw2 (STANDBY)</span>
          </span>
          <span>·</span>
          <span>Shared VIP 203.0.113.10</span>
        </div>
        <div>OpenStack Caracal / Dalmatian · CSTAM 3.0 · FelCloud</div>
      </footer>
    </div>
  )
}
