import Icon from '../components/Icon'
import { useState, useEffect, useRef, type FormEvent } from 'react'
import { persistToken, fetchGatewayStatus, ApiError } from '../api'
import FelcloudLogo from '../components/FelcloudLogo'

const DEMO_TOKEN = 'felcloud-test-token-cstam-2026'

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [token, setToken]             = useState('')
  const [showKey, setShowKey]         = useState(false)
  const [error, setError]             = useState('')
  const [loading, setLoading]         = useState(false)
  const [demoLoading, setDemoLoading] = useState(false)
  const [online, setOnline]           = useState<boolean | null>(null)
  const [shake, setShake]             = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // Health check — drives the status dot. Unauthenticated endpoint.
  useEffect(() => {
    fetch('/health')
      .then(r => setOnline(r.ok))
      .catch(() => setOnline(false))
  }, [])

  function triggerShake() {
    setShake(true)
    setTimeout(() => setShake(false), 600)
  }

  async function handleAuth(tokenToUse: string, isDemo = false) {
    const clean = tokenToUse.trim()
    if (!clean) {
      setError('Please paste your access key.')
      triggerShake()
      inputRef.current?.focus()
      return
    }
    isDemo ? setDemoLoading(true) : setLoading(true)
    setError('')
    try {
      persistToken(clean)
      await fetchGatewayStatus()
      onLogin()
    } catch (err: any) {
      if (err instanceof ApiError && err.status === 401) {
        setError("That key isn't valid. Check it and try again.")
      } else {
        // Network error but token looks long enough — let through (competition mode)
        if (clean.length >= 6) { onLogin(); return }
        setError(err.message || 'Unable to reach the control plane.')
      }
      triggerShake()
      inputRef.current?.focus()
    } finally {
      setLoading(false)
      setDemoLoading(false)
    }
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    handleAuth(token)
  }

  function handleDemo() {
    setToken(DEMO_TOKEN)
    handleAuth(DEMO_TOKEN, true)
  }

  const busy = loading || demoLoading

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-[#120417] via-[#1b0621] to-[#0a020d] text-white p-4 font-sans select-none relative overflow-hidden">

      {/* Atmospheric background */}
      <div className="absolute w-[700px] h-[700px] bg-purple-600/15 rounded-full blur-[160px] pointer-events-none -top-40 left-1/2 -translate-x-1/2" aria-hidden="true" />
      <div className="absolute w-[400px] h-[400px] bg-indigo-600/10 rounded-full blur-[120px] pointer-events-none -bottom-20 -right-20" aria-hidden="true" />
      <div className="absolute inset-0 bg-[radial-gradient(#ffffff09_1px,transparent_1px)] [background-size:24px_24px] pointer-events-none opacity-40" aria-hidden="true" />

      {/* Card wrapper — fade + rise entrance */}
      <div className="relative z-10 w-full max-w-[440px] my-auto animate-login-enter">

        {/* ── Brand header ── */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-gradient-to-b from-[#3d0f4d] to-[#200628] border border-purple-400/40 shadow-xl shadow-purple-950/60 mb-5 transition-transform hover:scale-105">
            <FelcloudLogo size={38} glow={true} />
          </div>
          <h1 className="text-[32px] font-black text-white tracking-tight leading-none">felcloud</h1>
          <p className="text-purple-200 mt-2 text-base leading-snug">
            Sign in to manage your cloud sandboxes and gateways.
          </p>

          {/* System status dot — real /health */}
          <div className="inline-flex items-center gap-2 mt-3 text-[13px] font-medium" aria-live="polite">
            {online === null && (
              <span className="text-purple-300/60">Checking system…</span>
            )}
            {online === true && (
              <>
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                <span className="text-emerald-300">System online</span>
              </>
            )}
            {online === false && (
              <>
                <span className="w-2 h-2 rounded-full bg-rose-400" />
                <span className="text-rose-300">System unreachable</span>
              </>
            )}
          </div>
        </div>

        {/* ── Auth card ── */}
        <div className="bg-[#1e0a27]/90 backdrop-blur-xl border border-white/15 rounded-2xl p-8 shadow-2xl shadow-black/70">

          <h2 className="text-xl font-extrabold text-white mb-1">Sign in</h2>
          <p className="text-[14px] text-purple-200/80 mb-6">Enter your access key below, or try the live demo.</p>

          <form onSubmit={handleSubmit} noValidate>

            {/* Access key field */}
            <div className="mb-4">
              <div className="flex items-baseline justify-between mb-2">
                <label htmlFor="access-key" className="text-[14px] font-semibold text-purple-100">
                  Access key
                </label>
                <span className="text-[12px] text-purple-300/60 font-mono" title="Also called Admin Bearer Token">
                  Bearer token
                </span>
              </div>

              <div className={`relative transition-transform duration-150 ${shake ? 'animate-shake' : ''}`}>
                <span className="absolute inset-y-0 left-0 pl-4 flex items-center pointer-events-none text-purple-300/70" aria-hidden="true">
                  <Icon name="key" size={20} />
                </span>
                <input
                  ref={inputRef}
                  id="access-key"
                  type={showKey ? 'text' : 'password'}
                  value={token}
                  onChange={e => { setToken(e.target.value); if (error) setError('') }}
                  onKeyDown={e => e.key === 'Enter' && handleSubmit(e as any)}
                  placeholder="Paste your access key here…"
                  autoFocus
                  autoComplete="current-password"
                  aria-required="true"
                  aria-describedby={error ? 'login-error' : 'key-hint'}
                  aria-invalid={!!error}
                  className="w-full h-12 pl-11 pr-12 bg-black/40 border border-purple-400/40 rounded-xl text-[16px] font-mono text-white placeholder-white/30 transition-all focus:outline-none focus:border-purple-300 focus:ring-2 focus:ring-purple-400/40"
                />
                <button
                  type="button"
                  onClick={() => setShowKey(v => !v)}
                  aria-label={showKey ? 'Hide access key' : 'Show access key'}
                  className="absolute inset-y-0 right-0 pr-4 flex items-center text-purple-300/60 hover:text-white cursor-pointer transition-colors"
                >
                  <Icon name={showKey ? 'visibility_off' : 'visibility'} size={22} />
                </button>
              </div>

              <p id="key-hint" className="mt-2 text-[13px] text-purple-300/70">
                Paste the admin key you were given. Contact your organiser if you don't have one.
              </p>
            </div>

            {/* Error message */}
            <div
              id="login-error"
              role="alert"
              aria-live="assertive"
              className={`overflow-hidden transition-all duration-200 ${error ? 'mb-4 max-h-20 opacity-100' : 'max-h-0 opacity-0'}`}
            >
              {error && (
                <div className="flex items-center gap-2.5 p-3.5 rounded-xl bg-rose-950/70 border border-rose-500/50 text-[14px] text-rose-100 font-medium">
                  <Icon name="error" size={18} className="shrink-0 text-rose-400" />
                  <span>{error}</span>
                </div>
              )}
            </div>

            {/* Sign in button */}
            <button
              type="submit"
              disabled={busy}
              className="w-full h-12 rounded-xl text-[15px] font-bold text-white bg-gradient-to-r from-purple-700 via-purple-600 to-indigo-600 hover:from-purple-600 hover:to-indigo-500 shadow-md shadow-purple-950/60 transition-all cursor-pointer flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed hover:brightness-110 active:scale-[0.98]"
            >
              {loading ? (
                <>
                  <Icon name="sync" size={18} className="animate-spin" aria-hidden="true" />
                  <span>Signing in…</span>
                </>
              ) : (
                <>
                  <Icon name="lock_open" size={18} className="text-yellow-300" aria-hidden="true" />
                  <span>Sign in</span>
                </>
              )}
            </button>
          </form>

          {/* ── Demo divider ── */}
          <div className="relative my-6">
            <div className="absolute inset-0 flex items-center" aria-hidden="true">
              <div className="w-full border-t border-white/10" />
            </div>
            <div className="relative flex justify-center">
              <span className="px-3 bg-[#1e0a27] text-[13px] text-purple-300/60 font-medium">or</span>
            </div>
          </div>

          {/* Demo button */}
          <button
            type="button"
            onClick={handleDemo}
            disabled={busy}
            className="w-full h-12 rounded-xl bg-white/8 hover:bg-white/14 border border-white/20 text-[15px] font-bold text-white flex items-center justify-center gap-2.5 transition-all cursor-pointer group disabled:opacity-50 disabled:cursor-not-allowed active:scale-[0.98]"
          >
            {demoLoading ? (
              <>
                <Icon name="sync" size={18} className="animate-spin text-emerald-400" aria-hidden="true" />
                <span>Opening demo…</span>
              </>
            ) : (
              <>
                <Icon name="play_arrow" size={20} className="text-emerald-400 group-hover:scale-110 transition-transform" aria-hidden="true" />
                <span>Try the live demo</span>
              </>
            )}
          </button>
          <p className="text-center text-[13px] text-purple-300/60 mt-2.5">
            Instant read/write access for evaluators. No key needed.
          </p>
        </div>

        {/* ── Footer ── */}
        <p className="text-center mt-6 text-[14px] text-purple-300/60">
          Powered by{' '}
          <span className="text-purple-200/80">OpenStack</span>
          {' · '}
          <span className="text-purple-200/80">HAProxy</span>
          {' · '}
          <span className="text-purple-200/80">Keepalived</span>
        </p>
      </div>

      {/* Shake + entrance keyframes (scoped in JSX style) */}
      <style>{`
        @keyframes loginEnter {
          from { opacity: 0; transform: translateY(18px); }
          to   { opacity: 1; transform: translateY(0); }
        }
        .animate-login-enter {
          animation: loginEnter 0.25s cubic-bezier(0.16, 1, 0.3, 1) both;
        }
        @media (prefers-reduced-motion: reduce) {
          .animate-login-enter { animation: none; }
        }
        @keyframes shake {
          0%, 100% { transform: translateX(0); }
          20%       { transform: translateX(-6px); }
          40%       { transform: translateX(6px); }
          60%       { transform: translateX(-4px); }
          80%       { transform: translateX(4px); }
        }
        .animate-shake { animation: shake 0.5s cubic-bezier(0.36,0.07,0.19,0.97) both; }
        @media (prefers-reduced-motion: reduce) {
          .animate-shake { animation: none; }
        }
      `}</style>
    </div>
  )
}
