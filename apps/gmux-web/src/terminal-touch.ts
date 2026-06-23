export const TERMINAL_TAP_MOVE_PX = 6

/**
 * Convert a touch-drag delta into xterm scrollback rows.
 *
 * xterm only scrolls whole rows, while touch movement is pixel-based. Keep a
 * fractional remainder between touchmove events for smooth slow drags, then
 * add distance-based acceleration so a longer phone swipe can traverse useful
 * scrollback without repeated tiny drags.
 */
export function acceleratedScrollRows({
  deltaY,
  totalDeltaY,
  cellHeight,
  remainder,
}: {
  deltaY: number
  totalDeltaY: number
  cellHeight: number
  remainder: number
}): { rows: number; remainder: number; exactRows: number } {
  const safeCellHeight = Math.max(1, cellHeight)
  const dragDistance = Math.max(0, Math.abs(totalDeltaY) - TERMINAL_TAP_MOVE_PX)
  // Terminal scrollback is row-granular and phones have short drag distance,
  // so keep some acceleration, but avoid runaway flicks.
  const acceleration = 1 + Math.min(3.5, dragDistance / 90)
  const exactRows = -(deltaY / safeCellHeight) * acceleration
  const rawRows = remainder + exactRows
  const rows = rawRows > 0 ? Math.floor(rawRows) : Math.ceil(rawRows)
  return { rows, remainder: rawRows - rows, exactRows }
}

export interface TerminalTouchSnapshot {
  x: number
  y: number
  scrollLeft: number
  scrollTop: number
  viewportY: number
}

export interface TerminalTouchPosition {
  x: number
  y: number
  scrollLeft: number
  scrollTop: number
  viewportY: number
}

export function terminalTouchMoved(
  start: TerminalTouchSnapshot,
  current: TerminalTouchPosition,
  thresholdPx = TERMINAL_TAP_MOVE_PX,
): boolean {
  return Math.abs(current.x - start.x) > thresholdPx
    || Math.abs(current.y - start.y) > thresholdPx
    || current.scrollLeft !== start.scrollLeft
    || current.scrollTop !== start.scrollTop
    || current.viewportY !== start.viewportY
}

export function shouldFocusTerminalFromTouch(
  active: boolean,
  alreadyMoved: boolean,
  start: TerminalTouchSnapshot,
  end: TerminalTouchPosition,
  thresholdPx = TERMINAL_TAP_MOVE_PX,
): boolean {
  if (!active || alreadyMoved) return false
  return !terminalTouchMoved(start, end, thresholdPx)
}
