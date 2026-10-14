import { useState, type FormEvent } from 'react'
import { persistToken, fetchConfig, ApiError } from '../api'

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!token.trim()) { setError('Token is required'); return }
    setLoading(true)
    setError('')
    try {
      // Validate token by hitting a protected endpoint
      persistToken(token.trim())
      await fetchConfig()
      onLogin()
    } catch (err: any) {
      if (err instanceof ApiError && err.status === 401) {
        setError('Invalid admin token')
      } else {
        setError(err.message || 'Connection failed')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center" style={{ background: 'linear-gradient(135deg, #0a0e1a 0%, #111827 50%, #1a1040 100%)' }}>
      <div className="animate-fade-in w-full max-w-md mx-4">
        {/* Logo and title */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl mb-4" style={{ background: 'linear-gradient(135deg, #6366f1, #a855f7)' }}>
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2L2 7l10 5 10-5-10-5z" />
              <path d="M2 17l10 5 10-5" />
              <path d="M2 12l10 5 10-5" />
            </svg>
          </div>
          <h1 className="text-3xl font-bold text-white tracking-tight">FelCloud</h1>
          <p className="text-[var(--color-text-secondary)] mt-1 text-sm">Administrator Dashboard</p>
        </div>

        {/* Login card */}
        <div className="rounded-2xl p-8 border" style={{ background: 'rgba(26, 31, 53, 0.8)', backdropFilter: 'blur(16px)', borderColor: 'var(--color-border)' }}>
          <form onSubmit={handleSubmit}>
            <label htmlFor="admin-token" className="block text-sm font-medium text-[var(--color-text-secondary)] mb-2">
              Admin Token
            </label>
            <input
              id="admin-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="Enter your ADMIN_TOKEN"
              autoFocus
              autoComplete="current-password"
              className="w-full px-4 py-3 rounded-xl text-white placeholder-[var(--color-text-muted)] border transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]"
              style={{ background: 'var(--color-bg-input)', borderColor: 'var(--color-border)' }}
            />

            {error && (
              <div className="mt-3 px-4 py-2 rounded-lg text-sm text-red-300" style={{ background: 'rgba(239, 68, 68, 0.15)' }}>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              id="login-submit"
              className="w-full mt-6 py-3 rounded-xl font-semibold text-white transition-all duration-200 hover:scale-[1.02] active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
              style={{ background: 'linear-gradient(135deg, #6366f1, #a855f7)' }}
            >
              {loading ? (
                <span className="inline-flex items-center gap-2">
                  <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" /><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" /></svg>
                  Authenticating…
                </span>
              ) : 'Sign In'}
            </button>
          </form>

          <p className="mt-4 text-xs text-center text-[var(--color-text-muted)]">
            Token stored in memory only. Never in localStorage.
          </p>
        </div>
      </div>
    </div>
  )
}
