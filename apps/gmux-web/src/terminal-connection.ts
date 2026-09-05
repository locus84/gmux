import type { TerminalSize } from './terminal-io'

export function shouldReassertReconnectSize(
  declaredCheckpoint: TerminalSize | null,
  logical: TerminalSize | null,
  currentPty: TerminalSize | null,
  localSession: boolean,
  awaitingResizeEcho: boolean,
): boolean {
  return localSession
    && !awaitingResizeEcho
    && declaredCheckpoint != null
    && logical != null
    && currentPty != null
    && currentPty.cols === declaredCheckpoint.cols
    && currentPty.rows === declaredCheckpoint.rows
    && declaredCheckpoint.rows === logical.rows
    && declaredCheckpoint.cols + 1 === logical.cols
}

/**
 * Fetch the daemon's current logical PTY size for an automatic reconnect.
 *
 * The runner hides a one-column shrink while no client is attached, without
 * exposing that implementation detail through session metadata. A fresh daemon
 * read returns the logical size that must be reasserted to restore the PTY and
 * trigger a TUI redraw (including inline image escape sequences).
 */
export async function fetchAuthoritativeReconnectSize(
  sessionId: string,
  fetcher: typeof fetch = fetch,
  timeoutMs = 2000,
): Promise<TerminalSize | null> {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetcher('/v1/sessions', {
      cache: 'no-store',
      signal: controller.signal,
    })
    if (!response.ok) return null

    const payload = await response.json() as {
      data?: Array<{ id?: string; terminal_cols?: number; terminal_rows?: number }>
    }
    const session = payload.data?.find(candidate => candidate.id === sessionId)
    const cols = session?.terminal_cols
    const rows = session?.terminal_rows
    if (!cols || !rows || cols < 1 || rows < 1) return null
    return { cols, rows }
  } catch {
    return null
  } finally {
    clearTimeout(timeout)
  }
}
