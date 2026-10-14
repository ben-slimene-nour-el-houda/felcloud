import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { setToken, getToken, clearToken, persistToken, restoreToken, forgetToken, ApiError } from './api'

describe('Token management', () => {
  beforeEach(() => {
    clearToken()
    sessionStorage.clear()
  })

  it('setToken / getToken round-trip', () => {
    setToken('abc123')
    expect(getToken()).toBe('abc123')
  })

  it('clearToken removes the token', () => {
    setToken('abc123')
    clearToken()
    expect(getToken()).toBe('')
  })

  it('persistToken stores in sessionStorage', () => {
    persistToken('secret-token')
    expect(getToken()).toBe('secret-token')
    expect(sessionStorage.getItem('fc_token')).toBe('secret-token')
  })

  it('restoreToken recovers from sessionStorage', () => {
    sessionStorage.setItem('fc_token', 'stored-token')
    const ok = restoreToken()
    expect(ok).toBe(true)
    expect(getToken()).toBe('stored-token')
  })

  it('restoreToken returns false when empty', () => {
    expect(restoreToken()).toBe(false)
  })

  it('forgetToken clears both memory and sessionStorage', () => {
    persistToken('to-forget')
    forgetToken()
    expect(getToken()).toBe('')
    expect(sessionStorage.getItem('fc_token')).toBeNull()
  })
})

describe('ApiError', () => {
  it('carries status and detail', () => {
    const err = new ApiError(422, 'bad', 'too long')
    expect(err.status).toBe(422)
    expect(err.message).toBe('bad')
    expect(err.detail).toBe('too long')
    expect(err).toBeInstanceOf(Error)
  })
})

describe('API fetch sends Authorization header', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    clearToken()
  })

  it('includes Bearer when token is set', async () => {
    setToken('my-token')
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ routing_domain: 'test.local' }),
    })

    const { fetchConfig } = await import('./api')
    await fetchConfig()

    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith(
      '/api/config',
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer my-token' }),
      }),
    )
  })
})
