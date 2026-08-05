import type { TerminalSize } from './terminal-io'

export interface TerminalSocketCloseContext<T> {
  closedSocket: T
  currentSocket: T | null
  intentionalClose: boolean
  disposed: boolean
  sessionStillCurrent: boolean
}

/** Only the active session's active socket may drive lost/reconnect state. */
export function shouldHandleTerminalSocketClose<T>({
  closedSocket,
  currentSocket,
  intentionalClose,
  disposed,
  sessionStillCurrent,
}: TerminalSocketCloseContext<T>): boolean {
  return closedSocket === currentSocket
    && !intentionalClose
    && !disposed
    && sessionStillCurrent
}

/**
 * Fetch the daemon's current logical PTY size for an automatic reconnect.
 *
 * The runner hides a one-column shrink while no client is attached, without
 * exposing that implementation detail through session metadata. A fresh daemon
 * read therefore returns the size that must be reasserted to restore the PTY.
 * Do not fall back to component/SSE caches here: they can be stale after the
 * page and its EventSource were suspended, which could undo another driver's
 * resize.
 */
export async function fetchAuthoritativeReconnectSize(
  sessionId: string,
  fetcher: typeof fetch = fetch,
): Promise<TerminalSize | null> {
  try {
    const response = await fetcher('/v1/sessions', { cache: 'no-store' })
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
  }
}
