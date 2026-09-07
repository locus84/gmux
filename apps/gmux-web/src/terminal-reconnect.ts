export const TERMINAL_RECONNECT_STALL_MS = 12_000
export const TERMINAL_REPLAY_COMMIT_STALL_MS = 60_000
export const TERMINAL_WAKE_COALESCE_MS = 2_000
export const TERMINAL_MEANINGFUL_SUSPEND_MS = 5_000

export function terminalReconnectDelay(attempt: number): number {
  return Math.min(500 * Math.pow(2, attempt), 8_000)
}

export function shouldReconnectAfterVisibility(
  hiddenAt: number | null,
  now: number,
  socketOpen: boolean,
): boolean {
  return !socketOpen || (hiddenAt !== null && now - hiddenAt >= TERMINAL_MEANINGFUL_SUSPEND_MS)
}

export function shouldReconnectFromPageShow(persisted: boolean): boolean {
  return persisted
}

export function shouldCoalesceTerminalWake(lastReconnectAt: number, now: number): boolean {
  return lastReconnectAt > 0 && now - lastReconnectAt < TERMINAL_WAKE_COALESCE_MS
}
