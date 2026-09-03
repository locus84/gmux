export const TERMINAL_TAP_MOVE_PX = 6

export function acceleratedScrollRows({
  deltaY, totalDeltaY, cellHeight, remainder,
}: {
  deltaY: number
  totalDeltaY: number
  cellHeight: number
  remainder: number
}): { rows: number; remainder: number; exactRows: number } {
  const safeCellHeight = Math.max(1, cellHeight)
  const dragDistance = Math.max(0, Math.abs(totalDeltaY) - TERMINAL_TAP_MOVE_PX)
  const acceleration = 1 + Math.min(3.5, dragDistance / 90)
  const exactRows = -(deltaY / safeCellHeight) * acceleration
  const rawRows = remainder + exactRows
  const rows = rawRows > 0 ? Math.floor(rawRows) : Math.ceil(rawRows)
  return { rows, remainder: rawRows - rows, exactRows }
}

export function decayScrollVelocity(velocity: number, elapsedMs: number): number {
  return velocity * Math.pow(0.90, Math.max(0, elapsedMs) / (1000 / 60))
}

export interface TerminalTouchSnapshot {
  x: number
  y: number
  scrollLeft: number
  scrollTop: number
}

export function terminalTouchMoved(
  start: TerminalTouchSnapshot,
  current: TerminalTouchSnapshot,
  thresholdPx = TERMINAL_TAP_MOVE_PX,
): boolean {
  return Math.abs(current.x - start.x) > thresholdPx
    || Math.abs(current.y - start.y) > thresholdPx
    || current.scrollLeft !== start.scrollLeft
    || current.scrollTop !== start.scrollTop
}

export function shouldFocusTerminalFromTouch(
  active: boolean,
  alreadyMoved: boolean,
  start: TerminalTouchSnapshot,
  end: TerminalTouchSnapshot,
  thresholdPx = TERMINAL_TAP_MOVE_PX,
): boolean {
  return active && !alreadyMoved && !terminalTouchMoved(start, end, thresholdPx)
}
