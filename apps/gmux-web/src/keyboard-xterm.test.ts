import { afterEach, describe, expect, it, vi } from 'vitest'

afterEach(() => vi.unstubAllGlobals())
import { Terminal } from '@xterm/xterm'
import { attachKeyboardHandler } from './keyboard'
import type { ResolvedKeybind } from './config'

const binding = {
  key: 'shift+enter', action: 'sendText', args: '\n',
  ctrl: false, shift: true, alt: false, meta: false, baseKey: 'enter',
} as ResolvedKeybind

function event(type: string, shiftKey = false): KeyboardEvent {
  return { type, key: 'Enter', keyCode: 13, charCode: 13,
    shiftKey, ctrlKey: false, altKey: false, metaKey: false,
    preventDefault() {}, stopPropagation() {},
  } as unknown as KeyboardEvent
}

describe('consumed Enter with real xterm keypress fallback', () => {
  for (const shiftRetained of [true, false]) {
    it(`sends one newline when keypress ${shiftRetained ? 'retains' : 'loses'} Shift`, () => {
      vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) })
      const term = new Terminal()
      const sent: string[] = []
      term.onData(data => sent.push(data))
      attachKeyboardHandler(term, data => sent.push(data), [binding])
      const core = (term as unknown as { _core: {
        _keyDown(event: KeyboardEvent): boolean
        _keyPress(event: KeyboardEvent): boolean
        _customKeyEventHandler(event: KeyboardEvent): boolean
      } })._core
      try {
        core._keyDown(event('keydown', true))
        core._keyPress(event('keypress', shiftRetained))
        core._customKeyEventHandler(event('keyup'))
        expect(sent).toEqual(['\n'])
        // Releasing Enter must not swallow a later ordinary Enter keypress.
        core._keyPress(event('keypress'))
        expect(sent).toEqual(['\n', '\r'])
        // Missing keyup (e.g. focus loss) must not swallow the next keydown.
        core._keyDown(event('keydown', true))
        expect(core._customKeyEventHandler(event('keydown'))).toBe(true)
      } finally {
        term.dispose()
      }
    })
  }
})
