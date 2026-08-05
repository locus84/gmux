import { describe, expect, it } from 'vitest'
import { acceleratedScrollRows, shouldFocusTerminalFromTouch, terminalTouchMoved } from './terminal-touch'

const start = {
  x: 100,
  y: 200,
  scrollLeft: 0,
  scrollTop: 0,
  viewportY: 10,
}

describe('terminal touch focus classification', () => {
  it('allows a stationary tap to focus the terminal input', () => {
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 103,
      y: 202,
      scrollLeft: 0,
      scrollTop: 0,
      viewportY: 10,
    })).toBe(true)
  })

  it('suppresses focus after finger movement beyond tap slop', () => {
    expect(terminalTouchMoved(start, {
      x: 100,
      y: 207,
      scrollLeft: 0,
      scrollTop: 0,
      viewportY: 10,
    })).toBe(true)

    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 100,
      y: 207,
      scrollLeft: 0,
      scrollTop: 0,
      viewportY: 10,
    })).toBe(false)
  })

  it('suppresses focus when the shell scrolled during the touch', () => {
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 101,
      y: 201,
      scrollLeft: 0,
      scrollTop: 24,
      viewportY: 10,
    })).toBe(false)
  })

  it('suppresses focus when xterm scrollback moved during the touch', () => {
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 101,
      y: 201,
      scrollLeft: 0,
      scrollTop: 0,
      viewportY: 12,
    })).toBe(false)
  })

  it('preserves an earlier moved flag even if the finger returns to the start point', () => {
    expect(shouldFocusTerminalFromTouch(true, true, start, {
      x: 100,
      y: 200,
      scrollLeft: 0,
      scrollTop: 0,
      viewportY: 10,
    })).toBe(false)
  })
})

describe('acceleratedScrollRows', () => {
  it('accumulates fractional row movement for slow drags', () => {
    let state = acceleratedScrollRows({ deltaY: -4, totalDeltaY: -10, cellHeight: 16, remainder: 0 })
    expect(state.rows).toBe(0)
    state = acceleratedScrollRows({ deltaY: -4, totalDeltaY: -14, cellHeight: 16, remainder: state.remainder })
    expect(state.rows).toBe(0)
    state = acceleratedScrollRows({ deltaY: -4, totalDeltaY: -18, cellHeight: 16, remainder: state.remainder })
    expect(state.rows).toBe(0)
    state = acceleratedScrollRows({ deltaY: -4, totalDeltaY: -22, cellHeight: 16, remainder: state.remainder })
    expect(state.rows).toBe(1)
  })

  it('accelerates longer drags without changing direction', () => {
    const short = acceleratedScrollRows({ deltaY: -16, totalDeltaY: -16, cellHeight: 16, remainder: 0 })
    const long = acceleratedScrollRows({ deltaY: -16, totalDeltaY: -220, cellHeight: 16, remainder: 0 })
    expect(short.rows).toBe(1)
    expect(long.rows).toBeGreaterThan(short.rows)

    const reverse = acceleratedScrollRows({ deltaY: 16, totalDeltaY: 220, cellHeight: 16, remainder: 0 })
    expect(reverse.rows).toBeLessThan(0)
  })
})
