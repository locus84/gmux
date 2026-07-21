import { describe, expect, it } from 'vitest'
import { shouldHandleTerminalSocketClose } from './terminal-connection'

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
