/**
 * Input Diagnostics Page
 *
 * A self-contained terminal with local echo (no WebSocket, no shell) that
 * instruments every input event on xterm's hidden textarea. Useful for
 * debugging keyboard issues on mobile devices with autocorrect, predictive
 * text, voice dictation, and third-party keyboards.
 *
 * Accessible at /_/input-diagnostics on any gmux instance.
 *
 * The page shows:
 * - A real xterm.js terminal with local echo (what you type appears on screen)
 * - A live event log showing every input/composition/beforeinput event
 * - A "Copy diagnostics" button so users can paste results into a bug report
 */

import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { TERM_THEME } from './terminal'

// ── Types ──

interface LogEntry {
  id: number
  time: string
  category: 'data' | 'input' | 'beforeinput' | 'composition' | 'textarea' | 'key' | 'selection' | 'info'
  message: string
  detail?: Record<string, unknown>
}

// ── Text / byte formatting ──

function toCodePoints(s: string): string {
  return [...s].map(c => {
    const cp = c.codePointAt(0)!
    return `U+${cp.toString(16).toUpperCase().padStart(cp > 0xffff ? 5 : 4, '0')}`
  }).join(' ')
}

function toUtf8Hex(s: string): string {
  return [...new TextEncoder().encode(s)]
    .map(b => b.toString(16).padStart(2, '0'))
    .join(' ')
}

function toHex(s: string): string {
  return [...s].map(c => {
    const cp = c.codePointAt(0)!
    return cp > 0xffff
      ? `U+${cp.toString(16).toUpperCase().padStart(5, '0')}`
      : cp > 0x7e || cp < 0x20
        ? `\\x${cp.toString(16).padStart(2, '0')}`
        : c
  }).join('')
}

function textDetail(s: string): Record<string, unknown> {
  return {
    text: s,
    printable: printable(s),
    codePoints: toCodePoints(s),
    utf8Hex: toUtf8Hex(s),
    length: s.length,
  }
}

function printable(s: string): string {
  return [...s].map(c => {
    const cp = c.codePointAt(0)!
    if (cp === 0x0a) return '\\n'
    if (cp === 0x0d) return '\\r'
    if (cp === 0x09) return '\\t'
    if (cp === 0x1b) return '\\e'
    if (cp === 0x7f) return '\\x7f'
    if (cp < 0x20) return `^${String.fromCharCode(cp + 64)}`
    return c
  }).join('')
}

// ── Local echo ──

/**
 * Wire up local echo on a Terminal. Characters received via onData are
 * echoed back with basic line editing (backspace, enter, ctrl-c, ctrl-u).
 * This simulates a shell prompt without needing a real PTY.
 */
function attachLocalEcho(term: Terminal): () => void {
  let line = ''
  let cursorPos = 0

  const prompt = () => {
    term.write('\r\n\x1b[36m$\x1b[0m ')
    line = ''
    cursorPos = 0
  }

  // Redraw the current line from cursor position (used after insert/delete mid-line)
  const redrawFromCursor = () => {
    // Write everything from cursorPos to end, then clear to EOL, then move back
    const tail = line.substring(cursorPos)
    term.write(tail + '\x1b[K')
    // Move cursor back to cursorPos
    if (tail.length > 0) {
      term.write(`\x1b[${tail.length}D`)
    }
  }

  prompt()

  const dispose = term.onData((data) => {
    // xterm's onData sends escape sequences as single strings (e.g. "\x1b[D"
    // for left arrow), so we handle the full string first before falling
    // through to per-character processing.
    if (data === '\x1b[D') {
      if (cursorPos > 0) { cursorPos--; term.write('\x1b[D') }
      return
    }
    if (data === '\x1b[C') {
      if (cursorPos < line.length) { cursorPos++; term.write('\x1b[C') }
      return
    }
    if (data === '\x1b[A' || data === '\x1b[B') {
      // Up/down arrows: ignore (no history)
      return
    }
    if (data.startsWith('\x1b')) {
      // Other escape sequences: ignore
      return
    }

    for (const ch of data) {
      const code = ch.codePointAt(0)!

      if (ch === '\r' || ch === '\n') {
        // Enter: show what was typed, new prompt
        term.write(`\r\n\x1b[2m(echo) ${printable(line)}\x1b[0m`)
        prompt()
      } else if (code === 0x7f || code === 0x08) {
        // Backspace
        if (cursorPos > 0) {
          line = line.substring(0, cursorPos - 1) + line.substring(cursorPos)
          cursorPos--
          term.write('\b')
          redrawFromCursor()
        }
      } else if (code === 0x03) {
        // Ctrl-C: cancel line
        term.write('^C')
        prompt()
      } else if (code === 0x15) {
        // Ctrl-U: clear line
        if (cursorPos > 0) {
          term.write(`\x1b[${cursorPos}D\x1b[K`)
          line = line.substring(cursorPos)
          cursorPos = 0
          redrawFromCursor()
        }
      } else if (code >= 0x20) {
        // Printable character: insert at cursor position
        line = line.substring(0, cursorPos) + ch + line.substring(cursorPos)
        cursorPos++
        term.write(ch)
        redrawFromCursor()
      }
    }
  })

  return () => dispose.dispose()
}

// ── Component ──

export default function InputDiagnostics() {
  const containerRef = useRef<HTMLDivElement>(null)
  const logRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const [entries, setEntries] = useState<LogEntry[]>([])
  const entryIdRef = useRef(0)
  const [autoScroll, setAutoScroll] = useState(true)

  const addEntry = useCallback((
    category: LogEntry['category'],
    message: string,
    detail?: Record<string, unknown>,
  ) => {
    const id = ++entryIdRef.current
    const now = new Date()
    const time = now.toISOString().split('T')[1].slice(0, -1)
    setEntries(prev => {
      const next = [...prev, { id, time, category, message, detail }]
      // Keep last 500 entries
      return next.length > 500 ? next.slice(-500) : next
    })
  }, [])

  // Auto-scroll the log
  useEffect(() => {
    if (autoScroll && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [entries, autoScroll])

  // Terminal setup
  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      theme: TERM_THEME,
      fontFamily: "'Fira Code', monospace",
      fontSize: 14,
      cursorBlink: true,
      scrollback: 100,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    fit.fit()
    termRef.current = term

    // Welcome message
    term.writeln('\x1b[1;36m── Input Diagnostics ──\x1b[0m')
    term.writeln('\x1b[2mType here to test keyboard input. Events are logged below.')
    term.writeln('For Korean/iPad: type 가, 한, 한국어 and copy the report.')
    term.writeln('We need to see whether xterm emits ㄱ + DEL + 가 or only ㄱ + ㅏ.\x1b[0m')

    // Attach local echo
    const disposeEcho = attachLocalEcho(term)

    // Instrument the hidden textarea
    const textarea = term.textarea

    // Log onData events (what xterm sends to the application). Production
    // TerminalView sends exactly these JS strings after UTF-8 encoding, so the
    // utf8Hex field is the raw PTY byte sequence we care about on iPad.
    const dataDispose = term.onData((data) => {
      addEntry('data', `onData: ${printable(data)}`, {
        ...textDetail(data),
        legacyRaw: toHex(data),
        textarea: textarea ? textareaSnapshot(textarea) : null,
      })
    })
    const disposeTextareaInstrumentation = textarea
      ? instrumentTextarea(textarea, addEntry)
      : () => {}

    if (textarea) {
      addEntry('info', 'xterm textarea attached', textareaSnapshot(textarea))
    }

    addEntry('info', 'Ready. Type to see events.', {
      userAgent: navigator.userAgent,
      platform: navigator.platform,
      touchPoints: navigator.maxTouchPoints,
    })

    const onResize = () => fit.fit()
    window.addEventListener('resize', onResize)
    window.visualViewport?.addEventListener('resize', onResize)

    term.focus()

    return () => {
      window.removeEventListener('resize', onResize)
      window.visualViewport?.removeEventListener('resize', onResize)
      disposeEcho()
      dataDispose.dispose()
      disposeTextareaInstrumentation()
      term.dispose()
      termRef.current = null
    }
  }, [addEntry])

  const handleCopy = useCallback(async () => {
    const report = buildReport(entries)
    try {
      await navigator.clipboard.writeText(report)
      addEntry('info', 'Diagnostics copied to clipboard!')
    } catch {
      // Fallback: select a textarea
      const ta = document.createElement('textarea')
      ta.value = report
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
      addEntry('info', 'Diagnostics copied to clipboard (fallback).')
    }
  }, [entries, addEntry])

  const handleClear = useCallback(() => {
    setEntries([])
    entryIdRef.current = 0
  }, [])

  const handleFocusTerm = useCallback(() => {
    termRef.current?.focus()
  }, [])

  return (
    <div class="diag-page">
      <div class="diag-header">
        <h1 class="diag-title">Input Diagnostics</h1>
        <a class="diag-back" href="/">← Back to gmux</a>
      </div>

      <div class="diag-terminal-section">
        <div class="diag-terminal-label">
          Terminal (local echo, no connection)
          <button class="diag-btn diag-btn-small" onClick={handleFocusTerm}>Focus</button>
        </div>
        <div class="diag-help">
          iPad Korean probe: focus the terminal, type <code>가</code>, <code>한</code>, <code>한국어</code>, then tap “Copy diagnostics”.
          In the report, <code>data.utf8Hex</code> should reveal whether DEL (<code>7f</code>) and composed Hangul bytes are present.
          Don’t type secrets here; the report includes raw typed text.
        </div>
        <div ref={containerRef} class="diag-terminal" />
      </div>

      <div class="diag-log-section">
        <div class="diag-log-toolbar">
          <span class="diag-log-label">Event Log ({entries.length})</span>
          <label class="diag-autoscroll">
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll((e.target as HTMLInputElement).checked)}
            />
            Auto-scroll
          </label>
          <button class="diag-btn" onClick={handleClear}>Clear</button>
          <button class="diag-btn diag-btn-primary" onClick={handleCopy}>
            Copy diagnostics
          </button>
        </div>
        <div ref={logRef} class="diag-log">
          {entries.map(entry => (
            <div key={entry.id} class={`diag-entry diag-cat-${entry.category}`}>
              <span class="diag-entry-time">{entry.time}</span>
              <span class={`diag-entry-cat`}>{entry.category}</span>
              <span class="diag-entry-msg">{entry.message}</span>
              {entry.detail && (
                <span class="diag-entry-detail">{JSON.stringify(entry.detail)}</span>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// ── Textarea instrumentation ──

type AddEntryFn = (
  category: LogEntry['category'],
  message: string,
  detail?: Record<string, unknown>,
) => void

function instrumentTextarea(textarea: HTMLTextAreaElement, addEntry: AddEntryFn): () => void {
  const disposers: Array<() => void> = []
  const listen = <K extends keyof HTMLElementEventMap>(
    type: K,
    listener: (ev: HTMLElementEventMap[K]) => void,
    options?: boolean | AddEventListenerOptions,
  ) => {
    textarea.addEventListener(type, listener as EventListener, options)
    disposers.push(() => textarea.removeEventListener(type, listener as EventListener, options))
  }
  const listenDocument = <K extends keyof DocumentEventMap>(
    type: K,
    listener: (ev: DocumentEventMap[K]) => void,
    options?: boolean | AddEventListenerOptions,
  ) => {
    document.addEventListener(type, listener as EventListener, options)
    disposers.push(() => document.removeEventListener(type, listener as EventListener, options))
  }

  // Track textarea value changes via polling (catches changes from any source)
  let lastValue = textarea.value
  const pollTimer = setInterval(() => {
    if (textarea.value !== lastValue) {
      addEntry('textarea', `value: "${printable(lastValue)}" → "${printable(textarea.value)}"`, {
        old: textDetail(lastValue),
        next: textDetail(textarea.value),
        ...textareaSnapshot(textarea),
      })
      lastValue = textarea.value
    }
  }, 30)
  disposers.push(() => clearInterval(pollTimer))

  // beforeinput: fires before the textarea is modified
  listen('beforeinput', (ev: InputEvent) => {
    const targetRanges = ev.getTargetRanges?.() ?? []
    const data = ev.data ?? ev.dataTransfer?.getData('text/plain') ?? ''
    addEntry('beforeinput', `${ev.inputType}`, {
      data: textDetail(data),
      inputType: ev.inputType,
      isComposing: ev.isComposing,
      cancelable: ev.cancelable,
      defaultPrevented: ev.defaultPrevented,
      eventPhase: ev.eventPhase,
      targetRanges: targetRanges.map(r => ({
        startOffset: r.startOffset,
        endOffset: r.endOffset,
      })),
      value: textDetail(textarea.value),
      ...textareaSnapshot(textarea),
    })
  }, true)

  // input: fires after the textarea is modified
  listen('input', (ev: Event) => {
    const iev = ev as InputEvent
    addEntry('input', `${iev.inputType ?? 'unknown'}`, {
      data: textDetail(iev.data ?? ''),
      inputType: iev.inputType,
      isComposing: iev.isComposing,
      cancelable: iev.cancelable,
      defaultPrevented: iev.defaultPrevented,
      eventPhase: iev.eventPhase,
      value: textDetail(textarea.value),
      ...textareaSnapshot(textarea),
    })
    lastValue = textarea.value // sync so poll doesn't double-report
  }, true)

  // Composition events
  for (const eventName of ['compositionstart', 'compositionupdate', 'compositionend'] as const) {
    listen(eventName, (ev: CompositionEvent) => {
      addEntry('composition', `${eventName}: "${printable(ev.data)}"`, {
        data: textDetail(ev.data),
        defaultPrevented: ev.defaultPrevented,
        eventPhase: ev.eventPhase,
        value: textDetail(textarea.value),
        ...textareaSnapshot(textarea),
      })
      lastValue = textarea.value
    }, true)
  }

  for (const eventName of ['keydown', 'keyup'] as const) {
    listen(eventName, (ev: KeyboardEvent) => {
      addEntry('key', `${eventName}: ${ev.key}`, {
        key: ev.key,
        code: ev.code,
        keyCode: ev.keyCode,
        isComposing: ev.isComposing,
        altKey: ev.altKey,
        ctrlKey: ev.ctrlKey,
        metaKey: ev.metaKey,
        shiftKey: ev.shiftKey,
        value: textDetail(textarea.value),
        ...textareaSnapshot(textarea),
      })
    }, true)
  }

  listenDocument('selectionchange', () => {
    if (document.activeElement !== textarea) return
    addEntry('selection', 'selectionchange', {
      value: textDetail(textarea.value),
      ...textareaSnapshot(textarea),
    })
  }, true)

  return () => {
    for (const dispose of disposers.splice(0)) dispose()
  }
}

function textareaSnapshot(textarea: HTMLTextAreaElement): Record<string, unknown> {
  const rect = textarea.getBoundingClientRect()
  const style = getComputedStyle(textarea)
  return {
    selStart: textarea.selectionStart,
    selEnd: textarea.selectionEnd,
    selectionDirection: textarea.selectionDirection,
    rect: {
      x: Math.round(rect.x),
      y: Math.round(rect.y),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
    },
    style: {
      opacity: style.opacity,
      color: style.color,
      caretColor: style.caretColor,
      position: style.position,
      left: style.left,
      top: style.top,
      width: style.width,
      height: style.height,
      fontSize: style.fontSize,
    },
  }
}

// ── Diagnostics report builder ──

function buildReport(entries: LogEntry[]): string {
  const lines: string[] = []
  lines.push('=== gmux Input Diagnostics Report ===')
  lines.push(`Date: ${new Date().toISOString()}`)
  lines.push(`User-Agent: ${navigator.userAgent}`)
  lines.push(`Platform: ${navigator.platform}`)
  lines.push(`Touch Points: ${navigator.maxTouchPoints}`)
  lines.push(`Screen: ${screen.width}x${screen.height} @ ${devicePixelRatio}x`)
  lines.push(`Viewport: ${window.innerWidth}x${window.innerHeight}`)
  if (window.visualViewport) {
    lines.push(`Visual Viewport: ${window.visualViewport.width}x${window.visualViewport.height}`)
  }
  lines.push('')
  lines.push(`--- Event Log (${entries.length} entries) ---`)
  lines.push('')

  for (const entry of entries) {
    let line = `[${entry.time}] [${entry.category.padEnd(12)}] ${entry.message}`
    if (entry.detail) {
      line += `  ${JSON.stringify(entry.detail)}`
    }
    lines.push(line)
  }

  lines.push('')
  lines.push('=== End Report ===')
  return lines.join('\n')
}
