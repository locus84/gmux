import { describe, expect, it } from 'vitest'
import { acceleratedScrollRows, decayScrollVelocity, shouldBlurTerminalAfterKeyboardClose, shouldFocusTerminalFromTouch, terminalTouchMoved } from './terminal-touch'

const start = {
  x: 100,
  y: 200,
  scrollLeft: 0,
  scrollTop: 0,
}

describe('terminal soft keyboard lifecycle', () => {
  it('blurs only the half-focused textarea after a touch keyboard closes', () => {
    expect(shouldBlurTerminalAfterKeyboardClose(true, false, true, true)).toBe(true)
    expect(shouldBlurTerminalAfterKeyboardClose(false, false, true, true)).toBe(false)
    expect(shouldBlurTerminalAfterKeyboardClose(true, true, true, true)).toBe(false)
    expect(shouldBlurTerminalAfterKeyboardClose(true, false, false, true)).toBe(false)
    expect(shouldBlurTerminalAfterKeyboardClose(true, false, true, false)).toBe(false)
  })
})

describe('terminal touch focus classification', () => {
  it('allows a stationary tap to focus the terminal input', () => {
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 103,
      y: 202,
      scrollLeft: 0,
      scrollTop: 0,
    })).toBe(true)
  })

  it('suppresses focus after finger movement beyond tap slop', () => {
    expect(terminalTouchMoved(start, {
      x: 100,
      y: 207,
      scrollLeft: 0,
      scrollTop: 0,
    })).toBe(true)

    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 100,
      y: 207,
      scrollLeft: 0,
      scrollTop: 0,
    })).toBe(false)
  })

  it('suppresses focus when the shell scrolled during the touch', () => {
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 101,
      y: 201,
      scrollLeft: 0,
      scrollTop: 24,
    })).toBe(false)
  })

  it('allows focus when background output advances xterm during the touch', () => {
    // Live output can advance xterm's viewport without finger movement, so
    // buffer position is deliberately not part of tap classification.
    expect(shouldFocusTerminalFromTouch(true, false, start, {
      x: 101,
      y: 201,
      scrollLeft: 0,
      scrollTop: 0,
    })).toBe(true)
  })

  it('preserves an earlier moved flag even if the finger returns to the start point', () => {
    expect(shouldFocusTerminalFromTouch(true, true, start, {
      x: 100,
      y: 200,
      scrollLeft: 0,
      scrollTop: 0,
    })).toBe(false)
  })
})

describe('scroll momentum', () => {
  it('decays by elapsed time rather than display refresh rate', () => {
    const oneFrame = decayScrollVelocity(1, 1000 / 60)
    const twoHalfFrames = decayScrollVelocity(decayScrollVelocity(1, 1000 / 120), 1000 / 120)
    expect(oneFrame).toBeCloseTo(0.9, 12)
    expect(twoHalfFrames).toBeCloseTo(oneFrame, 12)
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
