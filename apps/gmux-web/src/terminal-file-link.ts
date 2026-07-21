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
  /** Session-root-relative path accepted by the file API. */
  path: string
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

/** Find conservative, whitespace-delimited path candidates on one buffer line. */
export function findTerminalFileMatches(line: string, context: TerminalFileLinkContext): TerminalFileMatch[] {
  const matches: TerminalFileMatch[] = []
  for (const token of line.matchAll(/\S+/gu)) {
    const raw = token[0]
    const cleaned = cleanCandidate(raw)
    if (!cleaned) continue
    const path = resolveTerminalFilePath(cleaned.text, context)
    if (!path) continue
    matches.push({
      text: cleaned.text,
      start: (token.index ?? 0) + cleaned.offset,
      path,
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

function readWrappedBufferLine(term: Terminal, bufferLineNumber: number): { text: string; cells: BufferTextCell[] } | null {
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
  const cells: BufferTextCell[] = []
  for (let y = start; text.length < 2048; y++) {
    const current = buffer.getLine(y)
    if (!current) break
    const translated = readBufferLine(current, y)
    text += translated.text
    cells.push(...translated.cells)
    const next = buffer.getLine(y + 1)
    if (!next?.isWrapped) break
  }
  return { text, cells }
}

/**
 * Create an xterm provider whose session context is read lazily per line.
 * Capturing the context in each link ensures a link from old scrollback opens
 * against the session whose buffer produced it, even during a session switch.
 */
export function createTerminalFileLinkProvider(
  term: Terminal,
  getContext: () => TerminalFileLinkContext,
  fileHref: (sessionId: string, path: string) => string,
  openFile: (sessionId: string, path: string) => void,
): ILinkProvider {
  return {
    provideLinks(bufferLineNumber, callback) {
      const translated = readWrappedBufferLine(term, bufferLineNumber)
      if (!translated) {
        callback(undefined)
        return
      }

      const context = getContext()
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
          text: fileHref(context.sessionId, match.path),
          gmuxFile: true,
          range: {
            start: { x: startCell.startX + 1, y: startCell.y + 1 },
            end: { x: endCell.endX, y: endCell.y + 1 },
          },
          activate: () => openFile(context.sessionId, match.path),
        }]
      })
      callback(links)
    },
  }
}
