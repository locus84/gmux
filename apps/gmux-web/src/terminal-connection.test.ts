import { describe, expect, it } from 'vitest'
import { fetchAuthoritativeReconnectSize, shouldReassertReconnectSize } from './terminal-connection'

describe('shouldReassertReconnectSize', () => {
  it('requires positive evidence of one hidden local column with no newer resize', () => {
    const checkpoint = { cols: 79, rows: 24 }
    const logical = { cols: 80, rows: 24 }
    expect(shouldReassertReconnectSize(checkpoint, logical, checkpoint, true, false)).toBe(true)
    expect(shouldReassertReconnectSize({ cols: 80, rows: 24 }, logical, checkpoint, true, false)).toBe(false)
    expect(shouldReassertReconnectSize({ cols: 78, rows: 24 }, logical, checkpoint, true, false)).toBe(false)
    expect(shouldReassertReconnectSize({ cols: 79, rows: 23 }, logical, { cols: 79, rows: 23 }, true, false)).toBe(false)
    expect(shouldReassertReconnectSize(checkpoint, null, checkpoint, true, false)).toBe(false)
    expect(shouldReassertReconnectSize(checkpoint, logical, checkpoint, false, false)).toBe(false)
    expect(shouldReassertReconnectSize(null, logical, checkpoint, true, false)).toBe(false)
    expect(shouldReassertReconnectSize(checkpoint, logical, { cols: 100, rows: 24 }, true, false)).toBe(false)
    expect(shouldReassertReconnectSize(checkpoint, logical, checkpoint, true, true)).toBe(false)
  })
})

describe('fetchAuthoritativeReconnectSize', () => {
  it('reads the current session size without allowing a cached response', async () => {
    const fetcher = async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.cache).toBe('no-store')
      expect(init?.signal).toBeInstanceOf(AbortSignal)
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

  it('bounds a stalled fresh read', async () => {
    const fetcher = async (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      return await new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      })
    }
    await expect(fetchAuthoritativeReconnectSize('sess-1', fetcher, 1)).resolves.toBeNull()
  })
})
