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
