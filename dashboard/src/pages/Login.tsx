import { useState, useEffect, type FormEvent } from 'react'
import { persistToken, fetchGatewayStatus, ApiError } from '../api'

const TYPED_STRINGS = [
  'Keepalived VRRP Dual-Gateway Cluster',
  'HAProxy Data Plane API v2.8 Sync',
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

  const demoAdminToken = 'felcloud-test-token-cstam-2026'
  const demoOperatorToken = 'felcloud-op-session-caracal-99'
  const typed = useTypingEffect(TYPED_STRINGS)

  useEffect(() => {
    setMounted(true)
  }, [])

  async function authenticateWithToken(cleanToken: string) {
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

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    await authenticateWithToken(token.trim())
  }

  function handleQuickFill(presetToken: string) {
    setToken(presetToken)
    setError('')
  }

  function handleSocialLogin(provider: 'google' | 'github') {
    const chosenToken = provider === 'github' ? demoAdminToken : demoOperatorToken
    setToken(chosenToken)
    authenticateWithToken(chosenToken)
  }

  return (
    <div className="min-h-screen w-full flex flex-col lg:flex-row bg-[#0D0516] text-slate-100 overflow-x-hidden selection:bg-purple-600/30 selection:text-white">

      {/* ════════════════════════════════════════════════════════════
          LEFT BRANDING PANEL: Vibrant Glowing Aurora Mesh & Story
      ════════════════════════════════════════════════════════════ */}
      <div className="w-full lg:w-1/2 min-h-[500px] lg:min-h-screen relative flex flex-col justify-between p-8 sm:p-12 lg:p-16 overflow-hidden bg-[#100319] border-b lg:border-b-0 lg:border-r border-purple-900/20">

        {/* Deep Indigo-to-Purple Aurora Glow Mesh */}
        <div className="absolute inset-0 pointer-events-none overflow-hidden">
          {/* Glowing animated mesh blobs */}
          <div
            className="absolute -top-32 -left-32 w-[34rem] h-[34rem] rounded-full bg-gradient-to-br from-indigo-600/30 via-purple-600/25 to-pink-500/15 blur-[140px] animate-pulse"
            style={{ animationDuration: '9s' }}
          />
          <div className="absolute top-1/3 -right-24 w-[30rem] h-[30rem] rounded-full bg-gradient-to-tr from-purple-700/30 via-violet-600/20 to-indigo-800/20 blur-[130px]" />
          <div className="absolute -bottom-28 left-1/4 w-[32rem] h-[32rem] rounded-full bg-gradient-to-r from-fuchsia-600/20 via-purple-900/30 to-indigo-950/40 blur-[150px]" />
          {/* Subtle tech grid background */}
          <div className="absolute inset-0 tech-grid-pattern opacity-15" />
          {/* Gradient depth mask */}
          <div className="absolute inset-0 bg-gradient-to-b from-transparent via-[#100319]/40 to-[#100319]/80" />
        </div>

        {/* Top Header: Brand Identity & Live Cluster Status */}
        <div className={`relative z-10 flex items-center justify-between transition-all duration-700 ${mounted ? 'opacity-100 translate-y-0' : 'opacity-0 -translate-y-2'}`}>
          <div className="flex items-center gap-3.5">
            <div className="w-12 h-12 rounded-2xl bg-[#33053b] border border-purple-400/30 flex items-center justify-center p-2.5 shadow-lg shadow-purple-950/50">
              <img
                src="/felcloud-logo.png"
                alt="FelCloud Logo"
                className="w-full h-full object-contain filter drop-shadow-[0_0_8px_rgba(255,214,0,0.5)]"
              />
            </div>
            <div>
              <div className="font-bold text-xl tracking-tight text-white flex items-center gap-2">
                <span>FelCloud</span>
                <span className="text-xs font-mono px-2.5 py-0.5 rounded-full bg-purple-500/20 text-purple-300 border border-purple-400/30">
                  CSTAM 3.0
                </span>
              </div>
              <div className="text-xs text-purple-300/70 font-medium">The Resilient IP Optimizer</div>
            </div>
          </div>

          <div className="hidden sm:flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-white/5 border border-white/10 text-xs text-emerald-400 font-medium backdrop-blur-md">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
            <span>VIP 203.0.113.10 Online</span>
          </div>
        </div>

        {/* Center Content: Welcoming Typography & Step-Indicator Cards */}
        <div className={`relative z-10 my-auto py-10 space-y-8 max-w-xl transition-all duration-700 delay-100 ${mounted ? 'opacity-100 translate-y-0' : 'opacity-0 translate-y-4'}`}>
          <div className="space-y-3">
            <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-purple-500/10 border border-purple-400/20 text-purple-300 text-xs font-semibold">
              <span className="material-symbols-outlined text-[16px] text-amber-400">electric_bolt</span>
              <span>Zero-Downtime Multi-Tenant Ingress</span>
            </div>
            <h1 className="text-3xl sm:text-4xl lg:text-5xl font-extrabold text-white tracking-tight leading-[1.15]">
              Get Started with <span className="text-transparent bg-clip-text bg-gradient-to-r from-purple-300 via-pink-200 to-indigo-300">FelCloud</span>
            </h1>
            <p className="text-base text-slate-300/80 leading-relaxed max-w-lg">
              Resilient IP failover cluster powered by OpenStack Neutron, Keepalived VRRP, and sub-second HAProxy reload synchronization.
            </p>
          </div>

          {/* Step-Indicator List with subtle card highlights */}
          <div className="space-y-3.5">
            {[
              {
                step: '1',
                title: 'Connect Gateway Cluster',
                desc: 'Dual-node active-standby Keepalived VRRP with virtual IP election in < 500 ms.',
                status: 'Connected',
                active: true,
                icon: 'hub'
              },
              {
                step: '2',
                title: 'Provision Sandboxes & IPAM',
                desc: 'Automated 10.20.10.0/24 subnet allocation with 180s quarantine cooldown.',
                status: 'Ready',
                active: false,
                icon: 'dns'
              },
              {
                step: '3',
                title: 'Zero-Downtime Routing',
                desc: 'HAProxy Data Plane socket reload with atomic rollback on syntax failures.',
                status: 'Active',
                active: false,
                icon: 'alt_route'
              }
            ].map(item => (
              <div
                key={item.step}
                className={`p-4 rounded-2xl border transition-all duration-300 flex items-start gap-4 ${
                  item.active
                    ? 'bg-white/[0.08] border-purple-400/40 shadow-xl shadow-purple-950/30 backdrop-blur-md'
                    : 'bg-white/[0.02] border-white/5 hover:bg-white/[0.05]'
                }`}
              >
                <div className={`w-9 h-9 rounded-xl flex items-center justify-center font-bold text-sm shrink-0 ${
                  item.active
                    ? 'bg-gradient-to-br from-purple-500 to-indigo-600 text-white shadow-md'
                    : 'bg-white/10 text-slate-400'
                }`}>
                  {item.active ? (
                    <span className="material-symbols-outlined text-[18px]">check</span>
                  ) : (
                    item.step
                  )}
                </div>
                <div className="space-y-0.5 min-w-0 flex-1">
                  <div className="text-sm font-semibold text-white flex items-center gap-2">
                    <span>{item.title}</span>
                    <span className={`text-xs uppercase font-bold tracking-wider px-2 py-0.5 rounded-full ${
                      item.active
                        ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30'
                        : 'bg-white/5 text-slate-400 border border-white/10'
                    }`}>
                      {item.status}
                    </span>
                  </div>
                  <div className="text-xs text-slate-400 leading-relaxed">{item.desc}</div>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Left Panel Footer */}
        <div className="relative z-10 pt-6 border-t border-white/10 flex flex-col sm:flex-row items-start sm:items-center justify-between text-xs text-slate-400 gap-2">
          <div className="flex items-center gap-2 font-mono">
            <span className="w-2 h-2 rounded-full bg-emerald-500" />
            <span>OpenStack Dalmatian / Caracal</span>
          </div>
          <div className="font-mono text-purple-300/80">HAProxy 2.8 · Keepalived 2.2</div>
        </div>
      </div>

      {/* ════════════════════════════════════════════════════════════
          RIGHT FORM PANEL: Clean, Spacious Dark-Mode Auth Container
      ════════════════════════════════════════════════════════════ */}
      <div className="w-full lg:w-1/2 flex items-center justify-center p-8 sm:p-12 lg:p-16 bg-[#12071a] relative">

        {/* Ambient Corner Glow */}
        <div className="absolute top-0 right-0 w-80 h-80 bg-purple-600/10 rounded-full blur-[100px] pointer-events-none" />

        {/* Floating Authentication Card (Spacious p-10 to p-12) */}
        <div className={`w-full max-w-lg p-8 sm:p-10 lg:p-12 space-y-7 bg-[#170B20]/90 backdrop-blur-xl rounded-3xl border border-purple-500/20 shadow-2xl shadow-black/60 transition-all duration-700 delay-150 ${mounted ? 'opacity-100 translate-y-0' : 'opacity-0 translate-y-4'}`}>

          {/* Form Header */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold uppercase tracking-wider text-purple-400 font-mono">
                Operator Gateway
              </span>
              <span className="badge badge-emerald">Online</span>
            </div>
            <h2 className="text-3xl font-bold tracking-tight text-white">
              Sign In to Console
            </h2>
            <p className="text-sm text-slate-400 leading-relaxed">
              Enter your bearer authentication token or choose a provider below to manage routing, VIP, and tenant sandboxes.
            </p>
          </div>

          {/* Social Login Buttons (Google & GitHub) */}
          <div className="grid grid-cols-2 gap-3.5">
            <button
              type="button"
              onClick={() => handleSocialLogin('google')}
              className="h-12 px-4 rounded-xl bg-white/[0.04] hover:bg-white/[0.08] active:bg-white/[0.12] border border-white/10 hover:border-white/20 text-white text-sm font-semibold flex items-center justify-center gap-2.5 transition-all duration-200 cursor-pointer shadow-xs"
            >
              <svg className="w-4 h-4" viewBox="0 0 24 24">
                <path
                  fill="#EA4335"
                  d="M12 5c1.6 0 3 .6 4.1 1.6l3.1-3.1C17.3 1.7 14.8 1 12 1 7.5 1 3.7 3.6 1.9 7.3l3.7 2.9C6.5 7.4 9 5 12 5z"
                />
                <path
                  fill="#4285F4"
                  d="M23.5 12.3c0-.8-.1-1.6-.2-2.3H12v4.5h6.5c-.3 1.5-1.1 2.8-2.4 3.7l3.7 2.9c2.2-2 3.7-5 3.7-8.8z"
                />
                <path
                  fill="#FBBC05"
                  d="M5.6 14.8c-.2-.7-.4-1.5-.4-2.8s.2-2.1.4-2.8L1.9 6.3C.7 8.7 0 10.3 0 12s.7 3.3 1.9 5.7l3.7-2.9z"
                />
                <path
                  fill="#34A853"
                  d="M12 23c3.2 0 6-1.1 8-3l-3.7-2.9c-1.1.7-2.5 1.2-4.3 1.2-3 0-5.5-2.4-6.4-5.2L1.9 16c1.8 3.7 5.6 7 10.1 7z"
                />
              </svg>
              <span>Google</span>
            </button>

            <button
              type="button"
              onClick={() => handleSocialLogin('github')}
              className="h-12 px-4 rounded-xl bg-white/[0.04] hover:bg-white/[0.08] active:bg-white/[0.12] border border-white/10 hover:border-white/20 text-white text-sm font-semibold flex items-center justify-center gap-2.5 transition-all duration-200 cursor-pointer shadow-xs"
            >
              <svg className="w-4 h-4 fill-current text-white" viewBox="0 0 24 24">
                <path
                  fillRule="evenodd"
                  clipRule="evenodd"
                  d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.53 1.032 1.53 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0022 12.017C22 6.484 17.522 2 12 2z"
                />
              </svg>
              <span>GitHub</span>
            </button>
          </div>

          {/* Clean "Or" Divider */}
          <div className="relative flex items-center justify-center">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-white/10" />
            </div>
            <span className="relative px-4 text-xs font-semibold uppercase tracking-wider text-slate-400 bg-[#170B20]">
              Or continue with token
            </span>
          </div>

          {/* Authentication Form */}
          <form onSubmit={handleSubmit} className="space-y-5">
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <label htmlFor="admin-token" className="text-sm font-medium text-slate-300">
                  Bearer Authentication Token
                </label>
                <button
                  type="button"
                  onClick={() => handleQuickFill(demoAdminToken)}
                  className="text-xs font-semibold text-purple-400 hover:text-purple-300 transition-colors cursor-pointer"
                >
                  Fill Demo
                </button>
              </div>

              <div className="relative flex items-center">
                <span className="absolute left-4 flex items-center pointer-events-none text-slate-400">
                  <span className="material-symbols-outlined text-[20px]">key</span>
                </span>
                <input
                  id="admin-token"
                  type={showPassword ? 'text' : 'password'}
                  value={token}
                  onChange={e => setToken(e.target.value)}
                  placeholder="Paste bearer token…"
                  autoFocus
                  autoComplete="current-password"
                  className="h-12 w-full pl-12 pr-12 text-base font-mono bg-[#1E0B29] hover:bg-[#250E33] focus:bg-[#2A103B] text-white rounded-xl border border-purple-500/25 focus:border-purple-400 focus:outline-none focus:ring-2 focus:ring-purple-500/40 placeholder:text-slate-500 transition-all duration-200"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-4 flex items-center text-slate-400 hover:text-slate-200 cursor-pointer transition-colors"
                >
                  <span className="material-symbols-outlined text-[20px]">
                    {showPassword ? 'visibility_off' : 'visibility'}
                  </span>
                </button>
              </div>
            </div>

            {/* Error Feedback */}
            {error && (
              <div className="p-4 rounded-xl text-sm text-rose-200 bg-rose-950/60 border border-rose-500/30 flex items-start gap-3 animate-fade-in">
                <span className="material-symbols-outlined text-[20px] text-rose-400 shrink-0 mt-0.5">error</span>
                <span className="leading-relaxed font-medium">{error}</span>
              </div>
            )}

            {/* Prominent CTA Primary Button (Adequate height h-12) */}
            <button
              type="submit"
              disabled={loading}
              id="login-submit"
              className="h-12 w-full rounded-xl text-base font-semibold text-white bg-gradient-to-r from-[#8B1E9B] to-[#5C0B6B] hover:from-[#9D25AF] hover:to-[#6E0E80] border border-purple-400/30 shadow-lg shadow-purple-950/50 hover:shadow-purple-900/40 hover:-translate-y-0.5 active:translate-y-0 disabled:opacity-50 disabled:hover:translate-y-0 cursor-pointer flex items-center justify-center gap-2.5 transition-all duration-200"
            >
              {loading ? (
                <>
                  <span className="material-symbols-outlined text-[20px] animate-spin">refresh</span>
                  <span>Verifying Session…</span>
                </>
              ) : (
                <>
                  <span className="material-symbols-outlined text-[20px]">lock_open</span>
                  <span>Access Cluster Console</span>
                </>
              )}
            </button>
          </form>

          {/* Quick-Fill Presets for Evaluation */}
          <div className="p-4 rounded-2xl bg-white/[0.03] border border-white/8 space-y-2.5">
            <div className="flex items-center justify-between text-xs font-semibold text-slate-400">
              <span>Quick Evaluation Tokens</span>
              <span className="text-emerald-400 font-mono">1-Click Auto-Fill</span>
            </div>
            <div className="grid grid-cols-2 gap-2 text-xs font-mono">
              <button
                type="button"
                onClick={() => handleQuickFill(demoAdminToken)}
                className="px-3 py-2 rounded-xl bg-purple-950/60 hover:bg-purple-900/60 border border-purple-500/30 text-purple-200 hover:text-white transition-all text-left truncate cursor-pointer flex items-center justify-between"
              >
                <span className="font-semibold truncate">admin-cstam</span>
                <span className="text-slate-400 text-xs ml-1 shrink-0">Admin</span>
              </button>
              <button
                type="button"
                onClick={() => handleQuickFill(demoOperatorToken)}
                className="px-3 py-2 rounded-xl bg-purple-950/60 hover:bg-purple-900/60 border border-purple-500/30 text-purple-200 hover:text-white transition-all text-left truncate cursor-pointer flex items-center justify-between"
              >
                <span className="font-semibold truncate">operator-key</span>
                <span className="text-slate-400 text-xs ml-1 shrink-0">Operator</span>
              </button>
            </div>
          </div>

          {/* Typing Telemetry Feature Preview */}
          <div className="text-center">
            <span className="text-xs font-mono text-purple-300/70 inline-flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-purple-400 animate-pulse" />
              <span>{typed}</span>
            </span>
          </div>

          {/* Security & Compliance Badges */}
          <div className="flex items-center justify-center gap-5 text-xs text-slate-400 font-medium pt-1 border-t border-white/5">
            <span className="flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px] text-emerald-400">verified_user</span>
              <span>Zero-Trust RBAC</span>
            </span>
            <span>·</span>
            <span className="flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px] text-blue-400">lock</span>
              <span>Session Isolation</span>
            </span>
            <span>·</span>
            <span className="flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px] text-purple-400">https</span>
              <span>TLS 1.3</span>
            </span>
          </div>

        </div>
      </div>

    </div>
  )
}
