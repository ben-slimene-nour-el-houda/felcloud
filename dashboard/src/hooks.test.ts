import { describe, it, expect } from 'vitest'
import { formatDuration, isValidDnsLabel } from './hooks'

describe('formatDuration', () => {
  it('returns "Expired" for zero', () => {
    expect(formatDuration(0)).toBe('Expired')
  })

  it('returns "Expired" for negative', () => {
    expect(formatDuration(-10)).toBe('Expired')
  })

  it('formats seconds only', () => {
    expect(formatDuration(45)).toBe('45s')
  })

  it('formats minutes and seconds', () => {
    expect(formatDuration(125)).toBe('2m 5s')
  })

  it('formats hours, minutes, seconds', () => {
    expect(formatDuration(3661)).toBe('1h 1m 1s')
  })

  it('formats hours only when exact', () => {
    expect(formatDuration(7200)).toBe('2h')
  })

  it('formats minutes only when no seconds', () => {
    expect(formatDuration(300)).toBe('5m')
  })
})

describe('isValidDnsLabel', () => {
  it('accepts valid lowercase label', () => {
    expect(isValidDnsLabel('team-alpha')).toBe(true)
  })

  it('accepts single character', () => {
    expect(isValidDnsLabel('a')).toBe(true)
  })

  it('rejects starting with number', () => {
    expect(isValidDnsLabel('1team')).toBe(false)
  })

  it('rejects uppercase', () => {
    expect(isValidDnsLabel('Team')).toBe(false)
  })

  it('rejects empty string', () => {
    expect(isValidDnsLabel('')).toBe(false)
  })

  it('rejects trailing hyphen', () => {
    expect(isValidDnsLabel('team-')).toBe(false)
  })

  it('rejects 64+ characters', () => {
    expect(isValidDnsLabel('a'.repeat(64))).toBe(false)
  })

  it('accepts 63 characters', () => {
    expect(isValidDnsLabel('a'.repeat(63))).toBe(true)
  })
})
