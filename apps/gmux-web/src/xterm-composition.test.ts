import { describe, expect, it, vi } from 'vitest'
import { attachImeResidueGuard, flushPendingComposition, sendAfterFlushingComposition } from './xterm-composition'

function termWithHelper(helper: any = {}, textarea?: any, options?: any) {
  const dataListeners: Array<(data: string) => void> = []
  return {
    _core: { _compositionHelper: helper },
    textarea,
    options,
    onData(fn: (data: string) => void) {
      dataListeners.push(fn)
      return {
        dispose() {
          const i = dataListeners.indexOf(fn)
          if (i >= 0) dataListeners.splice(i, 1)
        },
      }
    },
    emitData(data: string) {
      for (const fn of [...dataListeners]) fn(data)
    },
  } as any
}

function createFakeTextarea() {
  const listeners: Record<string, { capture: EventListener[]; bubble: EventListener[] }> = {}
  const textarea = {
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    addEventListener: (type: string, fn: EventListener, opts?: boolean | AddEventListenerOptions) => {
      listeners[type] ??= { capture: [], bubble: [] }
      const capture = opts === true || (typeof opts === 'object' && !!opts.capture)
      listeners[type][capture ? 'capture' : 'bubble'].push(fn)
    },
    removeEventListener: (type: string, fn: EventListener, opts?: boolean | EventListenerOptions) => {
      const group = listeners[type]
      if (!group) return
      const capture = opts === true || (typeof opts === 'object' && !!opts.capture)
      const bucket = group[capture ? 'capture' : 'bubble']
      const i = bucket.indexOf(fn)
      if (i >= 0) bucket.splice(i, 1)
    },
    dispatch: (type: string) => {
      let stopped = false
      let prevented = false
      const ev = {
        type,
        preventDefault: () => { prevented = true },
        stopImmediatePropagation: () => { stopped = true },
      } as unknown as Event
      const group = listeners[type]
      for (const fn of group?.capture ?? []) fn(ev)
      if (!stopped) for (const fn of group?.bubble ?? []) fn(ev)
      return { stopped, prevented }
    },
    listenerCount: (type: string) => (listeners[type]?.capture.length ?? 0) + (listeners[type]?.bubble.length ?? 0),
  }
  return textarea
}

function liveHelper(extra: Record<string, unknown> = {}) {
  return {
    _isComposing: false,
    _isSendingComposition: false,
    _dataAlreadySent: '',
    _compositionPosition: { start: 0, end: 0 },
    _compositionSuffix: '',
    _preCompositionValue: '',
    get isComposing() { return this._isComposing },
    ...extra,
  } as any
}

describe('toolbar composition flushing', () => {
  it('finalizes active composition through xterm before toolbar submit is sent', () => {
    const events: string[] = []
    const textarea = { value: '안녕하세', selectionStart: 4, selectionEnd: 4 }
    const helper = liveHelper({
      _isComposing: true,
      _compositionPosition: { start: 0, end: 4 },
      _preCompositionValue: '',
      keydown: vi.fn((_: KeyboardEvent) => {
        events.push('composition')
        return true
      }),
    })
    const term = termWithHelper(helper, textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\r')

    expect(helper.keydown).toHaveBeenCalledOnce()
    expect(helper.keydown.mock.calls[0][0]).toMatchObject({ keyCode: 13 })
    expect(events).toEqual(['composition', '\r'])
    expect(textarea.value).toBe('')
    expect(textarea.selectionStart).toBe(0)
    expect(textarea.selectionEnd).toBe(0)
    expect(helper._isComposing).toBe(false)
    expect(helper._isSendingComposition).toBe(false)
    expect(helper._preCompositionValue).toBe('')
    expect(helper._compositionPosition).toEqual({ start: 0, end: 0 })
  })

  it('flushes xterm composition that is waiting for its delayed compositionend send', () => {
    const events: string[] = []
    const helper = liveHelper({
      _isSendingComposition: true,
      keydown: vi.fn((_: KeyboardEvent) => {
        events.push('composition')
        return true
      }),
    })
    const term = termWithHelper(helper)

    sendAfterFlushingComposition(term, data => events.push(data), '\r')

    expect(helper.keydown).toHaveBeenCalledOnce()
    expect(events).toEqual(['composition', '\r'])
  })

  it('clears idle IME residue before toolbar submit when no composition is active', () => {
    const events: string[] = []
    const keydown = vi.fn((_: KeyboardEvent) => true)
    const textarea = { value: 'already committed', selectionStart: 17, selectionEnd: 17 }
    const term = termWithHelper(liveHelper({ keydown }), textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\r')

    expect(keydown).not.toHaveBeenCalled()
    expect(events).toEqual(['\r'])
    expect(textarea.value).toBe('')
    expect(textarea.selectionStart).toBe(0)
    expect(textarea.selectionEnd).toBe(0)
  })

  it('does not flush or clear textarea for non-submit toolbar keys during live composition', () => {
    const events: string[] = []
    const textarea = { value: '오케이', selectionStart: 3, selectionEnd: 3 }
    const helper = liveHelper({
      _isComposing: true,
      _isSendingComposition: true,
      keydown: vi.fn((_: KeyboardEvent) => {
        events.push('composition')
        return true
      }),
    })
    const term = termWithHelper(helper, textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\x1b[A')

    expect(helper.keydown).not.toHaveBeenCalled()
    expect(events).toEqual(['\x1b[A'])
    expect(textarea.value).toBe('오케이')
  })

  it('clears idle IME residue before non-submit toolbar keys', () => {
    const events: string[] = []
    const textarea = { value: '오케이', selectionStart: 3, selectionEnd: 3 }
    const helper = liveHelper({
      _isSendingComposition: false,
      _dataAlreadySent: '오케이',
      _compositionPosition: { start: 0, end: 3 },
      keydown: vi.fn((_: KeyboardEvent) => {
        events.push('composition')
        return true
      }),
    })
    const term = termWithHelper(helper, textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\x1b[A')

    expect(helper.keydown).not.toHaveBeenCalled()
    expect(events).toEqual(['\x1b[A'])
    expect(textarea.value).toBe('')
    expect(helper._dataAlreadySent).toBe('')
    expect(helper._compositionPosition).toEqual({ start: 0, end: 0 })
  })

  it('does not clear while xterm has a delayed composition send pending', () => {
    const events: string[] = []
    const textarea = { value: '오케이', selectionStart: 3, selectionEnd: 3 }
    const helper = liveHelper({
      _isSendingComposition: true,
      keydown: vi.fn(() => false),
    })
    const term = termWithHelper(helper, textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\x1b[A')

    expect(helper.keydown).not.toHaveBeenCalled()
    expect(events).toEqual(['\x1b[A'])
    expect(textarea.value).toBe('오케이')
    expect(helper._isSendingComposition).toBe(true)
  })

  it('does not clear while xterm has a textarea diff timer pending', () => {
    const events: string[] = []
    const timer = setTimeout(() => {}, 1000) as unknown as number
    const textarea = { value: '오케이', selectionStart: 3, selectionEnd: 3 }
    const helper = liveHelper({ _textareaChangeTimer: timer })
    const term = termWithHelper(helper, textarea)

    sendAfterFlushingComposition(term, data => events.push(data), '\x1b[A')

    clearTimeout(timer)
    expect(events).toEqual(['\x1b[A'])
    expect(textarea.value).toBe('오케이')
    expect(helper._textareaChangeTimer).toBe(timer)
  })

  it('swallows the residual native IME cascade after forced submit', () => {
    vi.useFakeTimers()
    try {
      const events: string[] = []
      const textarea = createFakeTextarea()
      textarea.value = '오케이'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.addEventListener('compositionend', () => events.push('native compositionend'))
      textarea.addEventListener('input', () => events.push('native input'))
      const helper = liveHelper({
        _isComposing: true,
        keydown: vi.fn((_: KeyboardEvent) => {
          events.push('composition')
          return true
        }),
      })
      const term = termWithHelper(helper, textarea)

      sendAfterFlushingComposition(term, data => events.push(data), '\r')
      const compositionEnd = textarea.dispatch('compositionend')
      const input = textarea.dispatch('input')

      expect(events).toEqual(['composition', '\r'])
      expect(compositionEnd.stopped).toBe(true)
      expect(input.stopped).toBe(true)
      expect(textarea.value).toBe('')

      vi.runAllTimers()
      textarea.dispatch('input')
      expect(events).toEqual(['composition', '\r', 'native input'])
    } finally {
      vi.useRealTimers()
    }
  })

  it('is defensive when the xterm private helper is unavailable', () => {
    const events: string[] = []

    expect(flushPendingComposition({} as any)).toBe(false)
    sendAfterFlushingComposition({} as any, data => events.push(data), '\x1b[A')

    expect(events).toEqual(['\x1b[A'])
  })

  it('still sends toolbar input if the private composition helper throws', () => {
    const events: string[] = []
    const term = termWithHelper({
      isComposing: true,
      keydown: vi.fn(() => { throw new Error('private API changed') }),
    })

    expect(flushPendingComposition(term)).toBe(false)
    sendAfterFlushingComposition(term, data => events.push(data), '\r')

    expect(events).toEqual(['\r'])
  })
})

describe('attachImeResidueGuard', () => {
  it('uses a short default delay so mobile IME residue does not linger', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea)
      const dispose = attachImeResidueGuard(term)

      textarea.value = '오케이'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.dispatch('input')

      vi.advanceTimersByTime(49)
      expect(textarea.value).toBe('오케이')
      vi.advanceTimersByTime(1)
      expect(textarea.value).toBe('')

      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('clears committed IME residue after compositionend is idle', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea)
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = '오케이'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.dispatch('compositionstart')
      textarea.dispatch('compositionend')

      vi.advanceTimersByTime(149)
      expect(textarea.value).toBe('오케이')
      vi.advanceTimersByTime(1)
      expect(textarea.value).toBe('')
      expect(textarea.selectionStart).toBe(0)
      expect(textarea.selectionEnd).toBe(0)

      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not clear while a composition is active', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const helper = liveHelper({ _isComposing: true })
      const term = termWithHelper(helper, textarea)
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = 'ㅎ'
      textarea.selectionStart = textarea.selectionEnd = 1
      textarea.dispatch('compositionstart')
      textarea.dispatch('input')
      term.emitData('ㅎ')
      vi.advanceTimersByTime(500)

      expect(textarea.value).toBe('ㅎ')
      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('re-arms on keydown so clear cannot race xterm delayed textarea reads', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea)
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = 'abc'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.dispatch('keydown')
      vi.advanceTimersByTime(149)
      textarea.dispatch('keydown')
      vi.advanceTimersByTime(149)
      expect(textarea.value).toBe('abc')
      vi.advanceTimersByTime(1)
      expect(textarea.value).toBe('')

      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not clear selected textarea content used by xterm copy/screen-reader flows', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea)
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = 'selected text'
      textarea.selectionStart = 0
      textarea.selectionEnd = 8
      textarea.dispatch('input')
      vi.advanceTimersByTime(150)

      expect(textarea.value).toBe('selected text')
      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not attach in screenReaderMode', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea, { screenReaderMode: true })
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = '오케이'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.dispatch('input')
      vi.advanceTimersByTime(150)

      expect(textarea.value).toBe('오케이')
      expect(textarea.listenerCount('input')).toBe(0)
      dispose()
    } finally {
      vi.useRealTimers()
    }
  })

  it('dispose removes listeners and cancels pending clears', () => {
    vi.useFakeTimers()
    try {
      const textarea = createFakeTextarea()
      const term = termWithHelper(liveHelper(), textarea)
      const dispose = attachImeResidueGuard(term, 150)

      textarea.value = '오케이'
      textarea.selectionStart = textarea.selectionEnd = 3
      textarea.dispatch('input')
      expect(textarea.listenerCount('input')).toBe(1)
      dispose()
      expect(textarea.listenerCount('input')).toBe(0)
      vi.advanceTimersByTime(150)

      expect(textarea.value).toBe('오케이')
    } finally {
      vi.useRealTimers()
    }
  })
})
