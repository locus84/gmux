import { describe, expect, it } from 'vitest'
import { fetchAuthoritativeReconnectSize, shouldHandleTerminalSocketClose } from './terminal-connection'

const activeSocket = {}

function shouldHandle(overrides: Partial<Parameters<typeof shouldHandleTerminalSocketClose>[0]> = {}): boolean {
  return shouldHandleTerminalSocketClose({
    closedSocket: activeSocket,
    currentSocket: activeSocket,
    intentionalClose: false,
    disposed: false,
    sessionStillCurrent: true,
    ...overrides,
  })
}

describe('shouldHandleTerminalSocketClose', () => {
  it('handles a genuine close from the active socket', () => {
    expect(shouldHandle()).toBe(true)
  })

  it('ignores a stale close after a replacement socket becomes active', () => {
    expect(shouldHandle({ currentSocket: {} })).toBe(false)
  })

  it('ignores intentional, disposed, and previous-session closes', () => {
    expect(shouldHandle({ intentionalClose: true })).toBe(false)
    expect(shouldHandle({ disposed: true })).toBe(false)
    expect(shouldHandle({ sessionStillCurrent: false })).toBe(false)
  })
})

describe('fetchAuthoritativeReconnectSize', () => {
  it('reads the current session size without allowing a cached response', async () => {
    const fetcher = async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.cache).toBe('no-store')
      return new Response(JSON.stringify({
        data: [
          { id: 'other', terminal_cols: 70, terminal_rows: 20 },
          { id: 'sess-1', terminal_cols: 120, terminal_rows: 40 },
        ],
      }))
    }

    await expect(fetchAuthoritativeReconnectSize('sess-1', fetcher)).resolves.toEqual({ cols: 120, rows: 40 })
  })

  it('returns null for missing or invalid session dimensions', async () => {
    const fetcher = async () => new Response(JSON.stringify({
      data: [{ id: 'sess-1', terminal_cols: 0, terminal_rows: 24 }],
    }))

    await expect(fetchAuthoritativeReconnectSize('sess-1', fetcher)).resolves.toBeNull()
    await expect(fetchAuthoritativeReconnectSize('missing', fetcher)).resolves.toBeNull()
  })

  it('returns null when the fresh read fails', async () => {
    const fetcher = async () => { throw new Error('offline') }
    await expect(fetchAuthoritativeReconnectSize('sess-1', fetcher)).resolves.toBeNull()
  })
})
