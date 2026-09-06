import { describe, expect, it } from 'vitest'
import {
  shouldCoalesceTerminalWake,
  shouldReconnectAfterVisibility,
  shouldReconnectFromPageShow,
  terminalReconnectDelay,
} from './terminal-reconnect'

describe('terminal reconnect policy', () => {
  it('backs off failed attachments and caps retries', () => {
    expect([0, 1, 2, 3, 4, 5].map(terminalReconnectDelay)).toEqual([500, 1000, 2000, 4000, 8000, 8000])
  })

  it('does not replay a healthy socket after a brief tab switch', () => {
    expect(shouldReconnectAfterVisibility(1_000, 5_999, true)).toBe(false)
  })

  it('replaces a stale or meaningfully suspended socket', () => {
    expect(shouldReconnectAfterVisibility(1_000, 6_000, true)).toBe(true)
    expect(shouldReconnectAfterVisibility(null, 6_000, false)).toBe(true)
  })

  it('only reconnects pages restored from BFCache', () => {
    expect(shouldReconnectFromPageShow(false)).toBe(false)
    expect(shouldReconnectFromPageShow(true)).toBe(true)
  })

  it('coalesces clustered mobile wake events', () => {
    expect(shouldCoalesceTerminalWake(10_000, 11_999)).toBe(true)
    expect(shouldCoalesceTerminalWake(10_000, 12_000)).toBe(false)
    expect(shouldCoalesceTerminalWake(0, 100)).toBe(false)
  })
})
