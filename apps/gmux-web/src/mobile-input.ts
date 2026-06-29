/**
 * Mobile keyboard input fixes for xterm.js.
 *
 * Problem: mobile keyboards (iOS autocorrect, dictation, predictive text)
 * replace words in xterm's hidden textarea rather than appending. xterm.js
 * doesn't distinguish replacements from appends, so each replacement
 * re-sends text that was already on screen, causing cascading duplication.
 *
 * The replacement signal differs by platform:
 *
 *   iOS Safari: a single insertText (or insertReplacementText) with a
 *   non-collapsed selection (selectionStart < selectionEnd).
 *
 *   Android Chrome: a deleteContentBackward with non-collapsed selection,
 *   immediately followed by an insertText with collapsed selection. Same
 *   logical operation, split into two DOM events.
 *
 * Fix: two-phase interception.
 *
 *   beforeinput (textarea, capture): detect the replacement signal (iOS:
 *   non-collapsed selection on insertText; Android: deleteContentBackward
 *   with non-collapsed selection, carried forward to the next insertText).
 *   Send backspaces to erase from the replacement start to the end of the
 *   textarea.
 *
 *   input (container, capture): fires before xterm's handler on the textarea
 *   because capture goes parent-first. We stopImmediatePropagation() to
 *   prevent xterm from also sending ev.data, then send the replacement text
 *   plus the preserved suffix ourselves.
 *
 * Android has an additional complication: keydown events with keyCode 229
 * trigger xterm's CompositionHelper._handleAnyTextareaChanges, which uses
 * String.replace(oldValue, '') to diff the textarea. This works for pure
 * appends but produces garbage when the keyboard modifies the middle of the
 * string (the old value isn't a substring of the new value, so replace()
 * returns the entire textarea). We neutralize this by resetting
 * textarea.value to its pre-autocorrect state after sending the correct
 * data, so the deferred diff sees no change.
 *
 * This approach never calls preventDefault(), so it works regardless of
 * whether the browser considers beforeinput cancelable for the given
 * inputType and element type (a known cross-browser inconsistency).
 *
 * Assumption: the terminal cursor sits right after the last character in the
 * textarea. This holds for the normal mobile typing flow where replacements
 * fire immediately after typing. Mobile on-screen keyboards don't have arrow
 * keys, and autocorrect/dictation don't fire after cursor movement.
 *
 * See also: /_/input-diagnostics for collecting real event traces.
 */
import type { Terminal } from '@xterm/xterm'

type SendFn = (data: string) => void

type ActiveWebKitIme = {
  shouldSkip(data: string): boolean
  flushPending(): void
}

let activeWebKitIme: ActiveWebKitIme | null = null

interface PendingReplacement {
  newText: string
  suffix: string
  /** When set, reset textarea.value after sending to neutralize xterm's
   *  _handleAnyTextareaChanges deferred diff (Android keyCode-229 path). */
  resetValue?: string
}

interface PendingKoreanJamo {
  text: string
  sentAlready?: boolean
}

interface KoreanJamoState {
  buffer: string
  sent: string
}

/** Tracks a deleteContentBackward with non-collapsed selection so the
 *  immediately following insertText can be recognized as a replacement. */
interface TrackedDeletion {
  preDeleteValue: string
  deleteStart: number
  deleteEnd: number
}

const HANGUL_L = ['ㄱ', 'ㄲ', 'ㄴ', 'ㄷ', 'ㄸ', 'ㄹ', 'ㅁ', 'ㅂ', 'ㅃ', 'ㅅ', 'ㅆ', 'ㅇ', 'ㅈ', 'ㅉ', 'ㅊ', 'ㅋ', 'ㅌ', 'ㅍ', 'ㅎ']
const HANGUL_V = ['ㅏ', 'ㅐ', 'ㅑ', 'ㅒ', 'ㅓ', 'ㅔ', 'ㅕ', 'ㅖ', 'ㅗ', 'ㅘ', 'ㅙ', 'ㅚ', 'ㅛ', 'ㅜ', 'ㅝ', 'ㅞ', 'ㅟ', 'ㅠ', 'ㅡ', 'ㅢ', 'ㅣ']
const HANGUL_T = ['', 'ㄱ', 'ㄲ', 'ㄳ', 'ㄴ', 'ㄵ', 'ㄶ', 'ㄷ', 'ㄹ', 'ㄺ', 'ㄻ', 'ㄼ', 'ㄽ', 'ㄾ', 'ㄿ', 'ㅀ', 'ㅁ', 'ㅂ', 'ㅄ', 'ㅅ', 'ㅆ', 'ㅇ', 'ㅈ', 'ㅊ', 'ㅋ', 'ㅌ', 'ㅍ', 'ㅎ']
const L_INDEX = new Map(HANGUL_L.map((ch, i) => [ch, i]))
const V_INDEX = new Map(HANGUL_V.map((ch, i) => [ch, i]))
const T_INDEX = new Map(HANGUL_T.map((ch, i) => [ch, i]))
const COMBINE_V = new Map([
  ['ㅗㅏ', 'ㅘ'], ['ㅗㅐ', 'ㅙ'], ['ㅗㅣ', 'ㅚ'],
  ['ㅜㅓ', 'ㅝ'], ['ㅜㅔ', 'ㅞ'], ['ㅜㅣ', 'ㅟ'],
  ['ㅡㅣ', 'ㅢ'],
])
const COMBINE_T = new Map([
  ['ㄱㅅ', 'ㄳ'], ['ㄴㅈ', 'ㄵ'], ['ㄴㅎ', 'ㄶ'],
  ['ㄹㄱ', 'ㄺ'], ['ㄹㅁ', 'ㄻ'], ['ㄹㅂ', 'ㄼ'], ['ㄹㅅ', 'ㄽ'], ['ㄹㅌ', 'ㄾ'], ['ㄹㅍ', 'ㄿ'], ['ㄹㅎ', 'ㅀ'],
  ['ㅂㅅ', 'ㅄ'],
])

function isCompatibilityJamo(text: string): boolean {
  return text !== '' && [...text].every(ch => L_INDEX.has(ch) || V_INDEX.has(ch) || T_INDEX.has(ch))
}

function isAppleWebKit(): boolean {
  const ua = globalThis.navigator?.userAgent ?? ''
  return /AppleWebKit/i.test(ua) && !/Android/i.test(ua)
}

export function isAppleMobileWebKit(): boolean {
  const nav = globalThis.navigator
  const ua = nav?.userAgent ?? ''
  const platform = nav?.platform ?? ''
  return isAppleWebKit() && (
    /iPad|iPhone|iPod/i.test(ua) ||
    (platform === 'MacIntel' && (nav?.maxTouchPoints ?? 0) > 1)
  )
}

export function shouldDropMobileWebKitRawJamo(data: string): boolean {
  // Only the mobile/iPad WebKit workaround owns raw jamo. Desktop Korean IMEs
  // legitimately emit compatibility jamo as composition data; dropping it on
  // Mac Chrome/Edge/Safari makes repeated consonants like ㅇㅇ disappear.
  return isAppleMobileWebKit() && [...data].length === 1 && isCompatibilityJamo(data)
}

export function shouldSkipMobileWebKitImeData(data: string): boolean {
  return isAppleMobileWebKit() && (activeWebKitIme?.shouldSkip(data) ?? shouldDropMobileWebKitRawJamo(data))
}

export function flushMobileWebKitImePending(): void {
  if (isAppleMobileWebKit()) activeWebKitIme?.flushPending()
}

export function shouldBlockMobileWebKitImeKey(ev: KeyboardEvent): boolean {
  if (!isAppleMobileWebKit()) return false
  // Some iPad Korean IMEs report Backspace with keyCode 229; let the mobile
  // input handler process editing keys instead of xterm's generic IME block.
  if (ev.key === 'Backspace' || ev.key === 'Delete' || ev.keyCode === 8 || ev.keyCode === 46) return false
  return ev.keyCode === 229 || isCompatibilityJamo(ev.key ?? '')
}

function isHangulText(text: string): boolean {
  return text !== '' && [...text].some(ch => {
    const cp = ch.codePointAt(0) ?? 0
    return (cp >= 0x1100 && cp <= 0x11ff) ||
      (cp >= 0x3130 && cp <= 0x318f) ||
      (cp >= 0xac00 && cp <= 0xd7af) ||
      (cp >= 0xa960 && cp <= 0xa97f) ||
      (cp >= 0xd7b0 && cp <= 0xd7ff)
  })
}

function composeSyllable(l: string, v: string, t = ''): string {
  const li = L_INDEX.get(l)
  const vi = V_INDEX.get(v)
  const ti = T_INDEX.get(t)
  if (li === undefined || vi === undefined || ti === undefined) return l + v + t
  return String.fromCharCode(0xac00 + ((li * 21) + vi) * 28 + ti)
}

function decomposeHangulToCompatibilityJamo(text: string): string {
  let out = ''
  for (const ch of text.normalize('NFC')) {
    const cp = ch.codePointAt(0) ?? 0
    if (cp < 0xac00 || cp > 0xd7a3) {
      out += ch
      continue
    }
    const offset = cp - 0xac00
    const li = Math.floor(offset / (21 * 28))
    const vi = Math.floor((offset % (21 * 28)) / 28)
    const ti = offset % 28
    out += HANGUL_L[li] + HANGUL_V[vi] + (HANGUL_T[ti] ?? '')
  }
  return out
}

function mergeHangulEchoIntoJamoBuffer(buffer: string, echo: string): string {
  const echoJamo = decomposeHangulToCompatibilityJamo(echo)
  if (!echoJamo) return buffer
  const a = [...buffer]
  const b = [...echoJamo]
  let overlap = Math.min(a.length, b.length)
  while (overlap > 0) {
    if (a.slice(a.length - overlap).join('') === b.slice(0, overlap).join('')) break
    overlap--
  }
  return a.concat(b.slice(overlap)).join('')
}

function composeCompatibilityJamo(input: string): string {
  const chars = [...input]
  let out = ''
  let l = ''
  let v = ''
  let t = ''

  const flush = () => {
    if (l && v) out += composeSyllable(l, v, t)
    else out += l + v + t
    l = ''; v = ''; t = ''
  }

  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i]
    const next = chars[i + 1]
    const chIsL = L_INDEX.has(ch)
    const chIsV = V_INDEX.has(ch)

    if (!l) {
      if (chIsL) l = ch
      else out += ch
      continue
    }

    if (!v) {
      if (chIsV) v = ch
      else if (chIsL) { out += l; l = ch }
      else { out += l + ch; l = '' }
      continue
    }

    if (chIsV) {
      const combined = COMBINE_V.get(v + ch)
      if (combined && !t) v = combined
      else { flush(); out += ch }
      continue
    }

    if (!chIsL) {
      flush(); out += ch
      continue
    }

    if (!t) {
      if (next && V_INDEX.has(next)) {
        flush(); l = ch
      } else if (T_INDEX.has(ch)) {
        t = ch
      } else {
        flush(); l = ch
      }
      continue
    }

    const combined = COMBINE_T.get(t + ch)
    if (combined && !(next && V_INDEX.has(next))) {
      t = combined
    } else {
      flush(); l = ch
    }
  }

  flush()
  return out.normalize('NFC')
}

function stableCompatibilityJamoComposition(input: string): string {
  // Prefer visible preedit over hidden ambiguity on mobile: show a lone
  // consonant/vowel and rewrite it with backspaces if the next jamo changes
  // the syllable boundary (ㄱ → 가, 가ㄴ → 간 → 가나).
  return composeCompatibilityJamo(input)
}

function rewriteTail(previous: string, next: string): string {
  const a = [...previous]
  const b = [...next]
  let prefix = 0
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) prefix++
  return '\x7f'.repeat(a.length - prefix) + b.slice(prefix).join('')
}


export function createKoreanJamoInputFilter(send: SendFn): { consume(data: string): boolean; flush(): void; dispose(): void } {
  const state: KoreanJamoState = { buffer: '', sent: '' }

  const flush = () => {
    if (!state.buffer) return
    const committed = composeCompatibilityJamo(state.buffer)
    const tail = rewriteTail(state.sent, committed)
    if (tail) send(tail)
    state.buffer = ''
    state.sent = ''
  }


  return {
    consume(data: string): boolean {
      if ((data === '\x7f' || data === '\b') && state.buffer) {
        state.buffer = [...state.buffer].slice(0, -1).join('')
        const stable = stableCompatibilityJamoComposition(state.buffer)
        const chunk = rewriteTail(state.sent, stable)
        if (chunk) send(chunk)
        state.sent = stable
        return true
      }

      if (!isCompatibilityJamo(data)) {
        flush()
        return false
      }

      state.buffer += data
      const stable = stableCompatibilityJamoComposition(state.buffer)
      const chunk = rewriteTail(state.sent, stable)
      if (chunk) send(chunk)
      state.sent = stable
      return true
    },
    flush,
    dispose() {
      // No timer-based composition expiry; composition ends only on explicit
      // commit boundaries such as non-jamo input, Enter, or cleanup.
    },
  }
}

/**
 * Attach a handler that intercepts mobile keyboard word-replacement events
 * and translates them into terminal-compatible input sequences.
 *
 * Must be called after `term.open()` so `term.textarea` exists.
 * `container` should be the parent element of xterm's textarea (needed to
 * intercept input events in the capture phase before xterm sees them).
 * `send` should be the raw PTY send function (not sendInput, to avoid
 * ctrl/alt modifier interference; same convention as paste).
 *
 * Returns a cleanup function.
 */
export function attachMobileInputHandler(
  term: Terminal,
  container: HTMLElement,
  send: SendFn,
): () => void {
  const textarea = term.textarea
  if (!textarea) return () => {/* nothing to tear down */}

  // Autocorrect / word-replacement is a mobile-keyboard concern (iOS,
  // Android). On desktop, xterm.js manages the textarea selection
  // internally and may leave non-collapsed ranges that our handler would
  // misinterpret as autocorrect replacements, sending spurious backspaces.
  // Track the pointer type dynamically so tablet-mode switches are handled.
  const pointerQuery = window.matchMedia('(pointer: coarse)')
  let isTouchPrimary = pointerQuery.matches
  const onPointerChange = () => { isTouchPrimary = pointerQuery.matches }
  pointerQuery.addEventListener('change', onPointerChange)

  let pending: PendingReplacement | null = null
  let pendingKoreanJamo: PendingKoreanJamo | null = null
  let trackedDeletion: TrackedDeletion | null = null
  const koreanJamo: KoreanJamoState = { buffer: '', sent: '' }

  // WKWebView/Safari non-standard Korean IME path. Safari may emit raw jamo
  // plus native replacement syllables instead of standard composition events.
  // Keep the PTY stream visually in sync by sending the latest native syllable
  // immediately and rewriting it with backspaces if WebKit refines it.
  let wkComposing = false
  let wkSent = ''
  let wkExpectEcho = ''
  let suppressNextKoreanDelete = false
  let suppressEchoConsonantExtension = false

  const clearKoreanJamo = () => {
    koreanJamo.buffer = ''
    koreanJamo.sent = ''
    pendingKoreanJamo = null
    suppressEchoConsonantExtension = false
  }

  const wkCommit = () => {
    if (!wkComposing) return
    wkComposing = false
    wkSent = ''
  }

  const wkErase = () => {
    if (!wkComposing && !wkSent) return false
    if (wkSent) send('\x7f'.repeat([...wkSent].length))
    wkCommit()
    return true
  }

  const wkApply = (text: string, alreadySent = '') => {
    if (!text) return
    const previous = wkComposing ? wkSent : alreadySent
    const chunk = rewriteTail(previous, text)
    if (chunk) send(chunk)
    wkSent = text
    wkComposing = true
  }

  const shouldSkipWkData = (data: string) => {
    if ((data === '\x7f' || data === '\b') && wkSent !== '') return true
    if ([...data].length === 1 && isCompatibilityJamo(data)) return true
    if ([...data].length === 1 && isHangulText(data) && data === wkExpectEcho) {
      wkExpectEcho = ''
      return true
    }
    if (isCompatibilityJamo(data)) return true
    return wkComposing && [...data].length === 1 && isHangulText(data)
  }

  const wkController: ActiveWebKitIme = {
    shouldSkip: shouldSkipWkData,
    flushPending: wkCommit,
  }
  activeWebKitIme = wkController

  const flushKoreanJamo = () => {
    if (!koreanJamo.buffer) return
    const committed = composeCompatibilityJamo(koreanJamo.buffer)
    const chunk = rewriteTail(koreanJamo.sent, committed)
    if (chunk) send(chunk)
    koreanJamo.buffer = ''
    koreanJamo.sent = ''
    pendingKoreanJamo = null
    suppressEchoConsonantExtension = false
  }


  const updateKoreanJamo = (buffer: string) => {
    koreanJamo.buffer = buffer
    const stable = stableCompatibilityJamoComposition(koreanJamo.buffer)
    const chunk = rewriteTail(koreanJamo.sent, stable)
    if (chunk) send(chunk)
    koreanJamo.sent = stable
    pendingKoreanJamo = { text: stable, sentAlready: true }
  }

  const consumeKoreanJamo = (data: string) => {
    if (L_INDEX.has(data)) suppressEchoConsonantExtension = false
    updateKoreanJamo(koreanJamo.buffer + data)
  }

  const mergeKoreanEcho = (data: string) => {
    const merged = mergeHangulEchoIntoJamoBuffer(koreanJamo.buffer, data)
    const extension = [...merged].slice([...koreanJamo.buffer].length)
    if (suppressEchoConsonantExtension && extension.some(ch => L_INDEX.has(ch))) return
    updateKoreanJamo(merged)
  }

  const eraseKoreanJamo = () => {
    if (!koreanJamo.buffer) return false
    koreanJamo.buffer = [...koreanJamo.buffer].slice(0, -1).join('')
    const stable = stableCompatibilityJamoComposition(koreanJamo.buffer)
    const chunk = rewriteTail(koreanJamo.sent, stable)
    if (chunk) send(chunk)
    koreanJamo.sent = stable
    pendingKoreanJamo = stable ? { text: stable, sentAlready: true } : null
    suppressEchoConsonantExtension = true
    return true
  }

  /** Queue a replacement for phase 2 and send the necessary backspaces now. */
  const queueReplacement = (
    value: string,
    selStart: number,
    selEnd: number,
    newText: string,
    resetValue?: string,
  ) => {
    send('\x7f'.repeat(value.length - selStart))
    pending = { newText, suffix: value.substring(selEnd), resetValue }
  }

  /** Extract inserted text from a beforeinput event. */
  const resolveText = (ev: InputEvent) =>
    ev.data ?? ev.dataTransfer?.getData('text/plain') ?? ''

  // Phase 1: detect replacement and send backspaces.
  const onImeKey = (ev: KeyboardEvent) => {
    if (!isAppleWebKit()) return
    if (ev.key === 'Backspace' || ev.keyCode === 8) {
      if (wkErase() || eraseKoreanJamo()) {
        suppressNextKoreanDelete = true
        ev.preventDefault()
        ev.stopImmediatePropagation()
      }
      return
    }
    // On iOS/WKWebView, Korean IME keydowns can arrive as keyCode 229 after
    // beforeinput/onData. Stop xterm's DOM key path, but do not preventDefault:
    // WebKit's native IME still needs the keystroke. Backspace is handled above
    // because some iPad Korean IMEs report Backspace with keyCode 229.
    if (ev.keyCode === 229 || isCompatibilityJamo(ev.key ?? '')) {
      ev.stopImmediatePropagation()
      return
    }
    if (wkComposing) {
      if (ev.key === 'Shift' || ev.key === 'Control' || ev.key === 'Alt' || ev.key === 'Meta' || ev.key === 'CapsLock' || ev.key === 'AltGraph') return
      wkCommit()
    }
    if (koreanJamo.buffer) flushKoreanJamo()
  }

  const onBeforeInput = (ev: InputEvent) => {
    const appleMobile = isAppleMobileWebKit()
    if (!isTouchPrimary && !appleMobile) return

    // Snapshot and clear tracked deletion at the top; only the
    // deleteContentBackward branch may re-set it below.
    const deletion = trackedDeletion
    trackedDeletion = null

    // Android autocorrect: the keyboard splits word corrections into
    // deleteContentBackward (non-collapsed) + insertText (collapsed).
    // Track the deletion so we can combine it with the following insert.
    if (ev.inputType === 'deleteContentBackward') {
      if (appleMobile && suppressNextKoreanDelete) {
        suppressNextKoreanDelete = false
        ev.preventDefault()
        ev.stopImmediatePropagation()
        textarea.value = ''
        textarea.selectionStart = textarea.selectionEnd = 0
        return
      }
      if (appleMobile && (wkComposing || koreanJamo.buffer)) {
        ev.preventDefault()
        ev.stopImmediatePropagation()
        if (wkErase() || eraseKoreanJamo()) suppressNextKoreanDelete = true
        return
      }
      const start = textarea.selectionStart ?? 0
      const end = textarea.selectionEnd ?? start
      // Non-collapsed: potential Android autocorrect start. Track it.
      // Collapsed: normal backspace. Leave trackedDeletion null (already cleared).
      if (start < end) {
        trackedDeletion = { preDeleteValue: textarea.value, deleteStart: start, deleteEnd: end }
      }
      return
    }

    if (ev.inputType !== 'insertText' && ev.inputType !== 'insertReplacementText') {
      clearKoreanJamo()
      return
    }

    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? start
    const newText = resolveText(ev)

    if (appleMobile && ev.inputType === 'insertText' && start === end && isCompatibilityJamo(newText)) {
      if (wkComposing && !koreanJamo.buffer) wkCommit()
      ev.preventDefault()
      ev.stopImmediatePropagation()
      consumeKoreanJamo(newText)
      textarea.value = ''
      textarea.selectionStart = textarea.selectionEnd = 0
      return
    }

    if (isAppleWebKit()) {
      if ([...newText].length === 1 && isHangulText(newText) && (ev.inputType === 'insertText' || ev.inputType === 'insertReplacementText')) {
        wkExpectEcho = newText
        return
      }
      wkExpectEcho = ''
    }

    // xterm itself handles WKWebView/Safari Hangul IME replacement events via
    // our @xterm/xterm patch (xtermjs/xterm.js#5704). Do not treat them as
    // autocorrect replacements here, even if WebKit uses a non-collapsed
    // textarea selection during native composition.
    if (ev.inputType === 'insertReplacementText' && isHangulText(newText)) {
      // WebKit may report the first consonant as insertText (which we hold as
      // preedit) and then the native composed syllable as insertReplacementText
      // (가/나/...). Do not flush the held consonant as text; that is exactly
      // the ㄱ가 duplication seen on iPad. Hand the replacement event to xterm's
      // WK IME patch and discard our local fallback buffer.
      clearKoreanJamo()
      return
    }

    if (koreanJamo.buffer) flushKoreanJamo()

    // Android autocorrect phase 2: insertText immediately after a tracked
    // deletion completes the replacement pair.
    if (deletion && start === end) {
      if (newText) queueReplacement(
        deletion.preDeleteValue, deletion.deleteStart, deletion.deleteEnd,
        newText, deletion.preDeleteValue,
      )
      clearKoreanJamo()
      return
    }

    // Collapsed selection = normal append, let xterm handle it.
    if (start === end) {
      clearKoreanJamo()
      return
    }

    // iOS / single-event replacement: insertText or insertReplacementText
    // with non-collapsed selection.
    if (newText) queueReplacement(textarea.value, start, end, newText)
    clearKoreanJamo()
  }

  // Phase 2: intercept the input event before xterm, send replacement + suffix.
  // Registered on the container (parent) so capture phase fires before
  // xterm's capture-phase handler on the textarea itself.
  const onInput = (ev: Event) => {
    const inputEv = ev as InputEvent
    if (inputEv.inputType === 'deleteContentBackward' && suppressNextKoreanDelete) {
      suppressNextKoreanDelete = false
      ev.stopImmediatePropagation()
      inputEv.preventDefault?.()
      textarea.value = ''
      textarea.selectionStart = textarea.selectionEnd = 0
      return
    }

    if (pendingKoreanJamo) {
      if (inputEv.inputType === 'insertText' && isCompatibilityJamo(inputEv.data ?? '')) {
        const { text, sentAlready } = pendingKoreanJamo
        pendingKoreanJamo = null

        ev.stopImmediatePropagation()
        if (!sentAlready) {
          const chunk = rewriteTail(koreanJamo.sent, text)
          if (chunk) send(chunk)
          koreanJamo.sent = text
        }
        textarea.value = ''
        textarea.selectionStart = textarea.selectionEnd = 0
        return
      }
      pendingKoreanJamo = null
    }

    if (isAppleWebKit()) {
      const data = inputEv.data ?? ''

      if (data && inputEv.inputType === 'insertText' && isCompatibilityJamo(data)) {
        if (wkComposing && !koreanJamo.buffer) wkCommit()
        consumeKoreanJamo(data)
        ev.stopImmediatePropagation()
        inputEv.preventDefault?.()
        textarea.value = ''
        textarea.selectionStart = textarea.selectionEnd = 0
        return
      }

      if (data && (inputEv.inputType === 'insertText' || inputEv.inputType === 'insertReplacementText') && isHangulText(data)) {
        if (koreanJamo.buffer) {
          // Real iPad Safari can send consonants as raw jamo while vowels only
          // appear inside native replacement syllable echoes. Merge those
          // echoes back into the jamo buffer, but keep the buffer authoritative
          // so following consonants can still resolve finals (근/전/한).
          mergeKoreanEcho(data)
          ev.stopImmediatePropagation()
          inputEv.preventDefault?.()
          textarea.value = ''
          textarea.selectionStart = textarea.selectionEnd = 0
          return
        }
        const alreadySent = isCompatibilityJamo(data) ? '' : koreanJamo.sent
        clearKoreanJamo()
        wkApply(data, alreadySent)
        ev.stopImmediatePropagation()
        inputEv.preventDefault?.()
        textarea.value = ''
        textarea.selectionStart = textarea.selectionEnd = 0
        return
      }

      if (wkComposing && (inputEv.inputType === 'deleteContentBackward' || (inputEv.inputType === 'insertReplacementText' && !data))) {
        wkErase()
        ev.stopImmediatePropagation()
        inputEv.preventDefault?.()
        textarea.value = ''
        textarea.selectionStart = textarea.selectionEnd = 0
        return
      }

      if (wkComposing) wkCommit()
    }

    if (!pending) return
    const { newText, suffix, resetValue } = pending
    pending = null

    // Prevent xterm's _inputEvent from also sending ev.data.
    ev.stopImmediatePropagation()

    send(newText + suffix)

    // Android: reset textarea to the pre-autocorrect value. xterm's
    // CompositionHelper._handleAnyTextareaChanges (triggered by keydown 229)
    // captured this same value as oldValue and will diff against it in a
    // deferred setTimeout(0). By restoring it, the diff sees no change.
    if (resetValue !== undefined) {
      textarea.value = resetValue
      textarea.selectionStart = textarea.selectionEnd = resetValue.length
    }
  }

  const documentForTextarea = textarea.ownerDocument ?? globalThis.document
  const eventTargetsTextarea = (ev: Event) => {
    const path = typeof ev.composedPath === 'function' ? ev.composedPath() : []
    return ev.target === textarea || path.includes(textarea) || documentForTextarea?.activeElement === textarea
  }

  const onDocumentImeKey = (ev: KeyboardEvent) => {
    if (eventTargetsTextarea(ev)) onImeKey(ev)
  }
  const onDocumentBeforeInput = (ev: InputEvent) => {
    if (!eventTargetsTextarea(ev) || !isAppleWebKit()) return
    const data = resolveText(ev)
    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? start
    if (
      (ev.inputType === 'deleteContentBackward' && (koreanJamo.buffer || wkComposing)) ||
      ((ev.inputType === 'insertText' || ev.inputType === 'insertReplacementText') && isHangulText(data)) ||
      (ev.inputType === 'insertText' && start === end && isCompatibilityJamo(data))
    ) {
      onBeforeInput(ev)
    }
  }
  const onDocumentInput = (ev: Event) => {
    if (!eventTargetsTextarea(ev)) return
    const inputEv = ev as InputEvent
    if (pendingKoreanJamo || wkComposing || (inputEv.data && isHangulText(inputEv.data))) onInput(ev)
  }

  // Document-level capture is intentionally earlier than xterm's own capture
  // listeners on its hidden textarea. On iPadOS Safari the raw jamo can leak
  // through xterm before a parent/container listener gets a useful chance to
  // stop it, so Korean IME interception must sit at the top of the event path.
  documentForTextarea?.addEventListener('keydown', onDocumentImeKey, { capture: true })
  documentForTextarea?.addEventListener('keypress', onDocumentImeKey, { capture: true })
  documentForTextarea?.addEventListener('beforeinput', onDocumentBeforeInput, { capture: true })
  documentForTextarea?.addEventListener('input', onDocumentInput, { capture: true })

  container.addEventListener('keydown', onImeKey, { capture: true })
  container.addEventListener('keypress', onImeKey, { capture: true })
  container.addEventListener('beforeinput', onBeforeInput, { capture: true })
  container.addEventListener('input', onInput, { capture: true })

  return () => {
    clearKoreanJamo()
    if (activeWebKitIme === wkController) activeWebKitIme = null
    pointerQuery.removeEventListener('change', onPointerChange)
    documentForTextarea?.removeEventListener('keydown', onDocumentImeKey, { capture: true })
    documentForTextarea?.removeEventListener('keypress', onDocumentImeKey, { capture: true })
    documentForTextarea?.removeEventListener('beforeinput', onDocumentBeforeInput, { capture: true })
    documentForTextarea?.removeEventListener('input', onDocumentInput, { capture: true })
    container.removeEventListener('keydown', onImeKey, { capture: true })
    container.removeEventListener('keypress', onImeKey, { capture: true })
    container.removeEventListener('beforeinput', onBeforeInput, { capture: true })
    container.removeEventListener('input', onInput, { capture: true })
  }
}
