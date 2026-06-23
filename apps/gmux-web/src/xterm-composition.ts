import type { Terminal } from '@xterm/xterm'

interface XtermCompositionHelper {
  readonly isComposing?: boolean
  _isComposing?: boolean
  _isSendingComposition?: boolean
  _dataAlreadySent?: string
  _compositionPosition?: { start: number; end: number }
  _compositionSuffix?: string
  _preCompositionValue?: string
  _textareaChangeTimer?: number
  _compositionView?: { classList?: { remove?: (name: string) => void } }
  keydown?: (ev: KeyboardEvent) => boolean
}

interface XtermPrivateCore {
  _compositionHelper?: XtermCompositionHelper
}

interface XtermPrivateTerminal {
  _core?: XtermPrivateCore
  textarea?: HTMLTextAreaElement
  options?: { screenReaderMode?: boolean }
}

/**
 * Flush an active xterm IME composition before sending toolbar bytes.
 *
 * xterm's public API does not expose a composition commit hook. Its normal
 * keyboard path handles this by asking CompositionHelper.keydown() to finalize
 * the composition synchronously before Enter is processed; the mobile toolbar
 * bypasses that DOM keydown path and writes raw bytes directly. Reuse the same
 * internal path defensively so an in-progress Hangul syllable reaches the PTY
 * before the toolbar's Send button emits `\r`.
 */
export function flushPendingComposition(term: Terminal): boolean {
  const helper = compositionHelper(term)
  if ((!helper?.isComposing && !helper?._isSendingComposition) || typeof helper.keydown !== 'function') return false

  try {
    // keyCode 13 mirrors xterm's Enter handling: finalize composition now,
    // but do not send Enter itself. The caller sends the toolbar payload next.
    helper.keydown({ keyCode: 13 } as KeyboardEvent)
    return true
  } catch {
    // Private API changed or rejected the fake event. Do not block toolbar
    // input; falling back to the previous behavior is safer than eating keys.
    return false
  }
}

function compositionHelper(term: Terminal): XtermCompositionHelper | undefined {
  return (term as unknown as XtermPrivateTerminal)._core?._compositionHelper
}

function textarea(term: Terminal): HTMLTextAreaElement | undefined {
  return (term as unknown as XtermPrivateTerminal).textarea
}

function clearTextarea(term: Terminal): void {
  const ta = textarea(term)
  if (!ta) return
  ta.value = ''
  ta.selectionStart = ta.selectionEnd = 0
}

function resetCompositionState(term: Terminal): void {
  const helper = compositionHelper(term)
  if (!helper) return
  helper._isComposing = false
  helper._isSendingComposition = false
  helper._dataAlreadySent = ''
  helper._compositionSuffix = ''
  helper._preCompositionValue = ''
  if (helper._compositionPosition) {
    helper._compositionPosition.start = 0
    helper._compositionPosition.end = 0
  }
  if (helper._textareaChangeTimer !== undefined) {
    clearTimeout(helper._textareaChangeTimer)
    helper._textareaChangeTimer = undefined
  }
  helper._compositionView?.classList?.remove?.('active')
}

function isScreenReaderMode(term: Terminal): boolean {
  return Boolean((term as unknown as XtermPrivateTerminal).options?.screenReaderMode)
}

function hasTextareaSelection(ta: HTMLTextAreaElement): boolean {
  return ta.selectionStart !== ta.selectionEnd
}

function helperBusy(helper?: XtermCompositionHelper): boolean {
  return Boolean(
    helper?.isComposing ||
    helper?._isComposing ||
    helper?._isSendingComposition ||
    helper?._textareaChangeTimer !== undefined,
  )
}

function clearIdleTextareaResidue(term: Terminal): boolean {
  const ta = textarea(term)
  if (!ta?.value || hasTextareaSelection(ta)) return false
  const helper = compositionHelper(term)
  if (helperBusy(helper)) return false

  resetCompositionState(term)
  clearTextarea(term)
  return true
}

function suppressResidualImeEvents(term: Terminal): void {
  const ta = textarea(term)
  if (!ta || typeof ta.addEventListener !== 'function') return

  let done = false
  const cleanup = () => {
    if (done) return
    done = true
    clearTimeout(timer)
    ta.removeEventListener('compositionend', stopResidualEvent, true)
    ta.removeEventListener('beforeinput', stopResidualEvent, true)
    ta.removeEventListener('input', stopResidualEvent, true)
  }
  const stopResidualEvent = (ev: Event) => {
    ev.preventDefault()
    ev.stopImmediatePropagation()
    resetCompositionState(term)
    clearTextarea(term)
    setTimeout(cleanup, 0)
  }

  ta.addEventListener('compositionend', stopResidualEvent, { capture: true })
  ta.addEventListener('beforeinput', stopResidualEvent, { capture: true })
  ta.addEventListener('input', stopResidualEvent, { capture: true })
  const timer = setTimeout(cleanup, 250)
}

/**
 * Keep xterm's hidden helper textarea empty once IME input is idle.
 *
 * xterm intentionally keeps committed IME text in its helper textarea until
 * blur. On Android/Gboard and field-replacing input tools, that stale value can
 * be edited or re-committed later, duplicating prior Hangul text. Clear only
 * after xterm's own 0ms composition/diff timers have had time to read it. Keep
 * the default short so committed Korean text does not linger in the mobile
 * keyboard field after the user stops typing.
 */
export function attachImeResidueGuard(term: Terminal, delayMs = 50): () => void {
  const ta = textarea(term)
  if (!ta || isScreenReaderMode(term)) return () => {}

  let disposed = false
  let composing = false
  let timer: ReturnType<typeof setTimeout> | null = null
  let dataDisposable: { dispose: () => void } | undefined

  const cancel = () => {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }

  const isComposing = () => composing || helperBusy(compositionHelper(term))

  const clearIfIdle = () => {
    timer = null
    if (disposed || isComposing() || hasTextareaSelection(ta)) return
    if (ta.value === '') return
    ta.value = ''
    ta.selectionStart = ta.selectionEnd = 0
  }

  const schedule = () => {
    if (disposed || isComposing()) return
    cancel()
    timer = setTimeout(clearIfIdle, delayMs)
  }

  const handleCompositionStart = () => {
    composing = true
    cancel()
  }
  const handleCompositionEnd = () => {
    composing = false
    schedule()
  }
  const handleKeydown = () => {
    // Re-arm after every keydown so the clear cannot land between xterm's
    // keyCode-229 handling and its delayed textarea diff read.
    schedule()
  }
  const handleInput = () => schedule()

  ta.addEventListener('compositionstart', handleCompositionStart)
  ta.addEventListener('compositionend', handleCompositionEnd)
  ta.addEventListener('keydown', handleKeydown)
  ta.addEventListener('beforeinput', handleInput)
  ta.addEventListener('input', handleInput)
  dataDisposable = term.onData(() => schedule())

  return () => {
    disposed = true
    cancel()
    ta.removeEventListener('compositionstart', handleCompositionStart)
    ta.removeEventListener('compositionend', handleCompositionEnd)
    ta.removeEventListener('keydown', handleKeydown)
    ta.removeEventListener('beforeinput', handleInput)
    ta.removeEventListener('input', handleInput)
    dataDisposable?.dispose()
  }
}

export function sendAfterFlushingComposition(
  term: Terminal,
  send: (data: string) => void,
  data: string,
): void {
  const isSubmit = data.includes('\r')
  const flushed = isSubmit && flushPendingComposition(term)
  if (flushed) {
    suppressResidualImeEvents(term)
  } else {
    // When no live composition is being finalized, toolbar buttons should not
    // act on xterm's stale hidden textarea. Clear immediately if the helper is
    // truly idle; otherwise leave xterm's pending 0ms composition/diff reads
    // alone to avoid dropping a just-committed syllable.
    clearIdleTextareaResidue(term)
  }

  send(data)

  if (flushed) {
    // The browser can still deliver the native compositionend/input cascade
    // after the toolbar forced xterm to send the composition synchronously.
    // Empty xterm's private composition snapshot so that late cascade observes
    // an empty textarea and cannot send the same Hangul text a second time.
    resetCompositionState(term)
    clearTextarea(term)
  }
}
