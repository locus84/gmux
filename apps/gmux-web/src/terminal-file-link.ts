import type { IBufferLine, ILink, ILinkProvider, Terminal } from '@xterm/xterm'

export interface TerminalFileLinkContext {
  sessionId: string
  /** Root enforced by the session file API (`workspace_root`, then `cwd`). */
  root: string
  /** Process cwd used to resolve relative paths inside the root. */
  cwd: string
}

export interface TerminalFileMatch {
  /** Text painted in the terminal, excluding surrounding punctuation. */
  text: string
  /** Zero-based start column in the translated buffer line. */
  start: number
  /** Session-root-relative path, or a validated paste-image basename. */
  path: string
  pasteImage?: boolean
}

const leadingPunctuation = new Set(['(', '[', '{', '"', "'", '`', '*'])
const trailingPunctuation = new Set([')', ']', '}', '"', "'", '`', '*', ',', ';', '!', '?', '.', ':'])

function trimTrailingReference(token: string): string {
  return token.replace(/:\d+(?::\d+)?$/, '')
}

function cleanCandidate(raw: string): { text: string; offset: number } | null {
  let start = 0
  let end = raw.length
  while (start < end && leadingPunctuation.has(raw[start])) start++
  while (end > start && trailingPunctuation.has(raw[end - 1])) end--

  let text = raw.slice(start, end)
  text = trimTrailingReference(text)
  while (text && trailingPunctuation.has(text.at(-1)!)) text = text.slice(0, -1)
  text = text.replace(/\/+$/, '')
  return text ? { text, offset: start } : null
}

function normalizeSegments(base: string[], path: string): string[] | null {
  const segments = [...base]
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue
    if (segment === '..') {
      if (segments.length === 0) return null
      segments.pop()
      continue
    }
    segments.push(segment)
  }
  return segments
}

function cleanAbsolute(path: string): string {
  const normalized = normalizeSegments([], path)
  return `/${normalized?.join('/') ?? ''}`
}

function cleanAdvertisedPath(path: string): string | null {
  if (path === '~') return '~'
  if (path.startsWith('~/')) {
    const normalized = normalizeSegments([], path.slice(2))
    return normalized ? `~${normalized.length ? `/${normalized.join('/')}` : ''}` : null
  }
  return path.startsWith('/') ? cleanAbsolute(path) : null
}

function relativeToRoot(path: string, root: string): string | null {
  if (path === root) return ''
  const prefix = root === '/' ? '/' : `${root}/`
  return path.startsWith(prefix) ? path.slice(prefix.length) : null
}

/** Resolve terminal text to a path scoped by the existing session file API. */
export function resolveTerminalFilePath(text: string, context: TerminalFileLinkContext): string | null {
  if (!text.includes('/') || text.includes('://') || text.startsWith('//')) return null
  if (/[<>|*?\x00-\x1f]/u.test(text)) return null
  // Avoid treating fractions and similar numeric output as paths.
  if (!/[\p{L}._-]/u.test(text)) return null

  const root = cleanAdvertisedPath(context.root)
  const cwd = cleanAdvertisedPath(context.cwd)
  if (!root || !cwd) return null

  if (text.startsWith('/')) {
    // A tilde-advertised root cannot be safely compared with an absolute path
    // without knowing the daemon user's home directory.
    if (root.startsWith('~')) return null
    const absolute = cleanAbsolute(text)
    const relative = relativeToRoot(absolute, root)
    return relative || null
  }

  const cwdRelative = relativeToRoot(cwd, root)
  if (cwdRelative === null) return null
  const cwdBase = cwdRelative.split('/').filter(Boolean)
  const resolved = normalizeSegments(cwdBase, text)
  if (!resolved?.length) return null
  return resolved.join('/')
}

const pasteImageNameRe = /^paste-[1-9][0-9]*\.(?:png|jpe?g|gif|webp|avif|bmp)$/i

/** Recognize this session's canonical gmux clipboard path without exposing its directory. */
export function resolveTerminalPasteImageName(text: string, context?: TerminalFileLinkContext): string | null {
  if (!text.startsWith('/') || cleanAbsolute(text) !== text) return null
  const parts = text.split('/').filter(Boolean)
  const name = parts.at(-1) ?? ''
  const owner = parts.at(-2) ?? ''
  const marker = parts.at(-3) ?? ''
  const sessionId = context?.sessionId.split('@').slice(0, -1).join('@') || context?.sessionId
  if (marker !== 'gmux-pastes' || (sessionId && owner !== sessionId)) return null
  return pasteImageNameRe.test(name) ? name : null
}

/** Find conservative, whitespace-delimited path candidates on one buffer line. */
export function findTerminalFileMatches(line: string, context: TerminalFileLinkContext): TerminalFileMatch[] {
  const matches: TerminalFileMatch[] = []
  for (const token of line.matchAll(/\S+/gu)) {
    const raw = token[0]
    const cleaned = cleanCandidate(raw)
    if (!cleaned) continue
    const pasteImageName = resolveTerminalPasteImageName(cleaned.text, context)
    const path = pasteImageName ?? resolveTerminalFilePath(cleaned.text, context)
    if (!path) continue
    matches.push({
      text: cleaned.text,
      start: (token.index ?? 0) + cleaned.offset,
      path,
      ...(pasteImageName ? { pasteImage: true } : {}),
    })
  }
  return matches
}

interface BufferTextCell {
  startX: number
  endX: number
  y: number
}

function readBufferLine(line: IBufferLine, y: number): { text: string; cells: BufferTextCell[] } {
  // Tests and lightweight embedders may only expose translateToString.
  if (typeof line.getCell !== 'function') {
    const text = line.translateToString(true)
    return {
      text,
      cells: Array.from({ length: text.length }, (_, x) => ({ startX: x, endX: x + 1, y })),
    }
  }

  let text = ''
  const cells: BufferTextCell[] = []
  for (let x = 0; x < line.length; x++) {
    const cell = line.getCell(x)
    if (!cell || cell.getWidth() === 0) continue
    const chars = cell.getChars() || ' '
    text += chars
    // JS offsets count UTF-16 code units, while xterm ranges count cells.
    // Map every code unit in a combined/wide glyph to the same xterm cell.
    for (let i = 0; i < chars.length; i++) {
      cells.push({ startX: x, endX: x + Math.max(cell.getWidth(), 1), y })
    }
  }

  const trimmed = text.trimEnd()
  return { text: trimmed, cells: cells.slice(0, trimmed.length) }
}

interface BufferTextRange {
  text: string
  cells: BufferTextCell[]
  start: number
  end: number
}

export interface TerminalFileTarget {
  sessionId: string
  path: string
  pasteImage: boolean
  text: string
}

const visualPathFragmentRe = /^[A-Za-z0-9._~+%:@=/-]+$/
const manualWrapExtensions = [
  'avif', 'bmp', 'gif', 'jpeg', 'jpg', 'png', 'svg', 'webp',
  'css', 'go', 'html', 'js', 'json', 'jsx', 'md', 'mjs', 'py', 'rs', 'sh', 'toml', 'ts', 'tsx', 'txt', 'yaml', 'yml',
]
const maxManualWrapRows = 3

function readNativeBufferRange(term: Terminal, bufferLineNumber: number): BufferTextRange | null {
  const buffer = term.buffer.active
  let start = bufferLineNumber - 1
  let line = buffer.getLine(start)
  if (!line) return null

  while (line.isWrapped && start > 0) {
    const previous = buffer.getLine(start - 1)
    if (!previous) break
    line = previous
    start--
  }

  let text = ''
  let end = start
  const cells: BufferTextCell[] = []
  for (let y = start; text.length < 2048; y++) {
    const current = buffer.getLine(y)
    if (!current) break
    const translated = readBufferLine(current, y)
    text += translated.text
    cells.push(...translated.cells)
    end = y
    if (!buffer.getLine(y + 1)?.isWrapped) break
  }
  return { text, cells, start, end }
}

function manualExtensionContinuation(previousToken: string, nextToken: string): boolean {
  const extension = previousToken.match(/\.([A-Za-z0-9]{1,12})$/u)?.[1]?.toLowerCase()
  if (!extension) return false
  const continued = `${extension}${nextToken.toLowerCase()}`
  return manualWrapExtensions.some(candidate => candidate.startsWith(continued))
}

function isManualContinuation(previous: { text: string; cells: BufferTextCell[] }, nextText: string, cols: number): boolean {
  const previousToken = previous.text.trimEnd().match(/\S+$/)?.[0] ?? ''
  const nextToken = nextText.trimStart().match(/^\S+/)?.[0] ?? ''
  const visualEnd = previous.cells.at(-1)?.endX ?? previous.text.length
  const hasExtension = /\.[A-Za-z0-9]{1,12}$/u.test(previousToken)
  const continuesExtension = manualExtensionContinuation(previousToken, nextToken)
  return cols > 0
    && (visualEnd >= cols - 1 || continuesExtension)
    && nextText.length > nextText.trimStart().length
    && !!nextToken
    && visualPathFragmentRe.test(nextToken)
    && (!hasExtension || continuesExtension)
}

function combineManualRows(term: Terminal, start: number, end: number): BufferTextRange | null {
  const buffer = term.buffer.active
  let text = ''
  const cells: BufferTextCell[] = []
  for (let y = start; y <= end; y++) {
    const line = buffer.getLine(y)
    if (!line) return null
    const translated = readBufferLine(line, y)
    const offset = y === start ? 0 : translated.text.length - translated.text.trimStart().length
    text += translated.text.slice(offset)
    cells.push(...translated.cells.slice(offset))
  }
  return { text, cells, start, end }
}

function matchSpansRows(range: BufferTextRange, match: TerminalFileMatch): boolean {
  const first = range.cells[match.start]
  const last = range.cells[match.start + match.text.length - 1]
  return !!first && !!last && first.y !== last.y
}

function looksLikeCompleteManuallyWrappedFile(match: TerminalFileMatch): boolean {
  return match.pasteImage === true || /\.[A-Za-z0-9]{1,12}$/u.test(match.text)
}

/**
 * Read native xterm wraps first. For cursor-positioned TUI wraps, inspect at
 * most three rows and keep the join only when it forms a validated path that
 * actually crosses a row boundary. False negatives are safer than linking
 * unrelated terminal/input rows.
 */
function readBufferRange(term: Terminal, bufferLineNumber: number, context: TerminalFileLinkContext): BufferTextRange | null {
  const native = readNativeBufferRange(term, bufferLineNumber)
  if (!native) return null
  const buffer = term.buffer.active
  let start = native.start
  let end = native.end

  while (start > 0 && end - start + 1 < maxManualWrapRows) {
    const current = buffer.getLine(start)
    const previous = buffer.getLine(start - 1)
    if (!current || !previous || current.isWrapped) break
    const previousText = readBufferLine(previous, start - 1)
    const currentText = readBufferLine(current, start).text
    if (!isManualContinuation(previousText, currentText, term.cols)) break
    start--
  }
  while (end - start + 1 < maxManualWrapRows) {
    const current = buffer.getLine(end)
    const next = buffer.getLine(end + 1)
    if (!current || !next || next.isWrapped) break
    const currentText = readBufferLine(current, end)
    const nextText = readBufferLine(next, end + 1).text
    if (!isManualContinuation(currentText, nextText, term.cols)) break
    end++
  }

  if (start === native.start && end === native.end) return native
  const combined = combineManualRows(term, start, end)
  if (!combined) return native
  return findTerminalFileMatches(combined.text, context).some(match => (
    matchSpansRows(combined, match) && looksLikeCompleteManuallyWrappedFile(match)
  )) ? combined : null
}

function cursorIntersectsRange(term: Terminal, range: BufferTextRange): boolean {
  const buffer = term.buffer.active
  const cursorLine = buffer.baseY + buffer.cursorY
  return Number.isFinite(cursorLine) && cursorLine >= range.start && cursorLine <= range.end
}

function matchContainsCell(range: BufferTextRange, match: TerminalFileMatch, x: number, y: number): boolean {
  const first = range.cells[match.start]
  const last = range.cells[match.start + match.text.length - 1]
  if (!first || !last || y < first.y || y > last.y) return false
  if (first.y === last.y) return y === first.y && x >= first.startX && x < last.endX
  if (y === first.y) return x >= first.startX
  if (y === last.y) return x < last.endX
  return true
}

/** Resolve a mobile point without consulting xterm's private Linkifier cache. */
export function terminalFileTargetAtPoint(
  term: Terminal,
  context: TerminalFileLinkContext,
  clientX: number,
  clientY: number,
): TerminalFileTarget | null {
  const screen = term.element?.querySelector('.xterm-screen')
  const cell = term.dimensions?.css.cell
  if (!screen || typeof (screen as Element).getBoundingClientRect !== 'function' || !cell?.width || !cell.height) return null
  const rect = (screen as Element).getBoundingClientRect()
  const x = Math.floor((clientX - rect.left) / cell.width)
  const viewportRow = Math.floor((clientY - rect.top) / cell.height)
  if (x < 0 || x >= term.cols || viewportRow < 0 || viewportRow >= term.rows) return null

  const y = term.buffer.active.viewportY + viewportRow
  const range = readBufferRange(term, y + 1, context)
  if (!range || cursorIntersectsRange(term, range)) return null
  const match = findTerminalFileMatches(range.text, context).find(candidate => matchContainsCell(range, candidate, x, y))
  return match ? {
    sessionId: context.sessionId,
    path: match.path,
    pasteImage: !!match.pasteImage,
    text: match.text,
  } : null
}

/**
 * Create an xterm provider whose session context is read lazily per line.
 * Capturing the context in each link ensures a link from old scrollback opens
 * against the session whose buffer produced it, even during a session switch.
 */
export function createTerminalFileLinkProvider(
  term: Terminal,
  getContext: () => TerminalFileLinkContext,
  fileHref: (sessionId: string, path: string, pasteImage: boolean) => string,
  openFile: (sessionId: string, path: string, pasteImage: boolean) => void,
): ILinkProvider {
  return {
    provideLinks(bufferLineNumber, callback) {
      const context = getContext()
      const translated = readBufferRange(term, bufferLineNumber, context)
      if (!translated) {
        callback(undefined)
        return
      }

      if (cursorIntersectsRange(term, translated)) {
        callback(undefined)
        return
      }

      const matches = findTerminalFileMatches(translated.text, context)
      if (matches.length === 0) {
        callback(undefined)
        return
      }

      const links: ILink[] = matches.flatMap(match => {
        const startCell = translated.cells[match.start]
        const endCell = translated.cells[match.start + match.text.length - 1]
        if (!startCell || !endCell) return []
        return [{
          // xterm displays the buffer range, not this value. Keeping the
          // generated viewer URL here lets the existing mobile long-press
          // sheet inspect/copy/open a durable target without agent knowledge.
          text: fileHref(context.sessionId, match.path, !!match.pasteImage),
          gmuxFile: true,
          range: {
            start: { x: startCell.startX + 1, y: startCell.y + 1 },
            end: { x: endCell.endX, y: endCell.y + 1 },
          },
          activate: () => openFile(context.sessionId, match.path, !!match.pasteImage),
        }]
      })
      callback(links)
    },
  }
}
