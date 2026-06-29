import type { Terminal } from '@xterm/xterm'
import { isTouchDevice } from './touch'

let installed = false

/**
 * Force xterm's image addon onto its HTMLImageElement decode path on touch
 * devices.
 *
 * The addon's iTerm2 inline-image handler prefers
 * `createImageBitmap(blob, { resizeWidth, resizeHeight })` when available.
 * That path is fast on desktop, but mobile WebKit has historically been much
 * more fragile around resized ImageBitmap/canvas compositing and can leave a
 * solid black image rectangle. When `createImageBitmap` is unavailable, the
 * addon already falls back to `Image` + 2D canvas `drawImage`, which is slower
 * but much better exercised on iOS/iPadOS.
 *
 * Keep this global, page-local shim narrow: only touch devices get it, and it
 * runs before any terminal image output is parsed.
 */
export function installTouchInlineImageDecodeFallback(): void {
  if (installed) return
  installed = true

  if (!isTouchDevice()) return
  if (!window.createImageBitmap) return

  try {
    Object.defineProperty(window, 'createImageBitmap', {
      configurable: true,
      writable: true,
      value: undefined,
    })
  } catch {
    try {
      ;(window as unknown as { createImageBitmap?: typeof window.createImageBitmap }).createImageBitmap = undefined
    } catch {
      // If the browser refuses the shim, the terminal still works; it just uses
      // the browser's ImageBitmap path and may hit the black-rectangle bug.
    }
  }
}

const IIP_FILE_PREFIX = '\x1b]1337;File='
const MAX_IIP_HEADER_CHARS = 2048
const MAX_IMAGE_PROBE_BASE64_CHARS = 8192
const COMMON_HEADER_PROBE_BASE64_CHARS = 160

interface ImageDimensions {
  width: number
  height: number
}

export interface InlineImageSanitizer {
  transform(data: Uint8Array): Uint8Array[]
  reset(): void
}

/**
 * Clamp iTerm2 inline-image display dimensions before xterm/addon-image sees
 * them. The addon reserves terminal rows from the decoded display bitmap size,
 * so CSS cannot fix mobile cases where a single image consumes dozens of rows
 * or creates a huge horizontal tile grid.
 *
 * The sanitizer only rewrites the small OSC 1337 File header. It preserves the
 * base64 payload as bytes and streams it once enough leading image bytes are
 * available to choose the constraining dimension while preserving aspect ratio.
 */
export function createTouchInlineImageSanitizer(term: Terminal): InlineImageSanitizer {
  if (!isTouchDevice()) {
    return {
      transform(data) { return data.length ? [data] : [] },
      reset() {},
    }
  }

  let pending = ''

  const encode = (value: string): Uint8Array => {
    const out = new Uint8Array(value.length)
    for (let i = 0; i < value.length; i++) out[i] = value.charCodeAt(i) & 0xff
    return out
  }

  const decode = (data: Uint8Array): string => {
    let out = ''
    for (let i = 0; i < data.length; i++) out += String.fromCharCode(data[i])
    return out
  }

  const flushNormalPrefix = (value: string, out: Uint8Array[]): string => {
    const keep = suffixPrefixLength(value, IIP_FILE_PREFIX)
    if (keep === value.length) return value
    const emit = value.slice(0, value.length - keep)
    if (emit) out.push(encode(emit))
    return value.slice(value.length - keep)
  }

  const rewriteHeader = (header: string, probeBase64: string): string => {
    const colon = header.length - 1
    const argText = header.slice(IIP_FILE_PREFIX.length, colon)
    const args = argText
      .split(';')
      .filter(Boolean)
      .filter(arg => {
        const key = arg.slice(0, arg.indexOf('=') === -1 ? arg.length : arg.indexOf('='))
        return key !== 'width' && key !== 'height' && key !== 'preserveAspectRatio'
      })

    const maxRows = mobileInlineImageMaxRows(term.rows)
    const dims = imageDimensionsFromBase64(probeBase64)
    const constraint = dims ? chooseInlineImageConstraint(term, dims, maxRows) : `height=${maxRows}`

    args.push(constraint)
    args.push('preserveAspectRatio=1')
    return `${IIP_FILE_PREFIX}${args.join(';')}:`
  }

  return {
    transform(data: Uint8Array): Uint8Array[] {
      if (!data.length) return []

      const out: Uint8Array[] = []
      let value = pending + decode(data)
      pending = ''

      while (value.length) {
        const start = value.indexOf(IIP_FILE_PREFIX)
        if (start === -1) {
          pending = flushNormalPrefix(value, out)
          break
        }

        if (start > 0) {
          out.push(encode(value.slice(0, start)))
          value = value.slice(start)
        }

        const colon = value.indexOf(':', IIP_FILE_PREFIX.length)
        if (colon === -1) {
          if (value.length > MAX_IIP_HEADER_CHARS) {
            out.push(encode(value))
            value = ''
          } else {
            pending = value
            value = ''
          }
          break
        }

        const probeAvailable = value.slice(colon + 1, colon + 1 + MAX_IMAGE_PROBE_BASE64_CHARS)
        const hasTerminator = findOscTerminator(probeAvailable) !== -1
        const probeEnd = hasTerminator ? findOscTerminator(probeAvailable) : probeAvailable.length
        const probeBase64 = probeAvailable.slice(0, probeEnd)
        const dims = imageDimensionsFromBase64(probeBase64)
        const shouldWaitForProbe = !dims
          && !hasTerminator
          && probeBase64.length < COMMON_HEADER_PROBE_BASE64_CHARS
          && value.length < colon + 1 + MAX_IMAGE_PROBE_BASE64_CHARS

        if (shouldWaitForProbe) {
          pending = value
          value = ''
          break
        }

        out.push(encode(rewriteHeader(value.slice(0, colon + 1), probeBase64)))
        value = value.slice(colon + 1)
      }

      return out
    },

    reset(): void {
      pending = ''
    },
  }
}

function mobileInlineImageMaxRows(rows: number): number {
  return Math.max(4, Math.min(18, Math.floor(rows * 0.45) || 18))
}

function chooseInlineImageConstraint(term: Terminal, dims: ImageDimensions, maxRows: number): string {
  const activeBuffer = term.buffer.active as unknown as { cursorX?: number }
  const availableCols = Math.max(1, term.cols - (activeBuffer.cursorX ?? 0))
  const cellWidth = term.dimensions?.css.cell.width || 1
  const cellHeight = term.dimensions?.css.cell.height || 2
  const rowsAtFullWidth = availableCols * (dims.height / dims.width) * (cellWidth / cellHeight)
  return rowsAtFullWidth <= maxRows ? `width=${availableCols}` : `height=${maxRows}`
}

function suffixPrefixLength(value: string, prefix: string): number {
  const max = Math.min(value.length, prefix.length - 1)
  for (let len = max; len > 0; len--) {
    if (value.slice(value.length - len) === prefix.slice(0, len)) return len
  }
  return 0
}

function findOscTerminator(value: string): number {
  const bel = value.indexOf('\x07')
  const st = value.indexOf('\x1b\\')
  if (bel === -1) return st
  if (st === -1) return bel
  return Math.min(bel, st)
}

function imageDimensionsFromBase64(value: string): ImageDimensions | null {
  const usable = value.replace(/[^A-Za-z0-9+/=]/g, '')
  const alignedLength = usable.length - (usable.length % 4)
  if (alignedLength < 16) return null

  try {
    const raw = atob(usable.slice(0, alignedLength))
    const bytes = new Uint8Array(raw.length)
    for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i)
    return pngDimensions(bytes) ?? gifDimensions(bytes) ?? jpegDimensions(bytes)
  } catch {
    return null
  }
}

function pngDimensions(bytes: Uint8Array): ImageDimensions | null {
  if (bytes.length < 24) return null
  if (bytes[0] !== 0x89 || bytes[1] !== 0x50 || bytes[2] !== 0x4e || bytes[3] !== 0x47) return null
  return {
    width: readUint32BE(bytes, 16),
    height: readUint32BE(bytes, 20),
  }
}

function gifDimensions(bytes: Uint8Array): ImageDimensions | null {
  if (bytes.length < 10) return null
  if (bytes[0] !== 0x47 || bytes[1] !== 0x49 || bytes[2] !== 0x46) return null
  return {
    width: bytes[6] | (bytes[7] << 8),
    height: bytes[8] | (bytes[9] << 8),
  }
}

function jpegDimensions(bytes: Uint8Array): ImageDimensions | null {
  if (bytes.length < 4 || bytes[0] !== 0xff || bytes[1] !== 0xd8) return null
  let i = 2
  while (i + 9 < bytes.length) {
    while (i < bytes.length && bytes[i] !== 0xff) i++
    if (i + 3 >= bytes.length) return null
    const marker = bytes[i + 1]
    if (marker === 0xd8 || marker === 0xd9) {
      i += 2
      continue
    }
    const length = (bytes[i + 2] << 8) | bytes[i + 3]
    if (length < 2 || i + 1 + length >= bytes.length) return null
    if ((marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) || (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf)) {
      return {
        height: (bytes[i + 5] << 8) | bytes[i + 6],
        width: (bytes[i + 7] << 8) | bytes[i + 8],
      }
    }
    i += 2 + length
  }
  return null
}

function readUint32BE(bytes: Uint8Array, offset: number): number {
  return ((bytes[offset] << 24) >>> 0) + (bytes[offset + 1] << 16) + (bytes[offset + 2] << 8) + bytes[offset + 3]
}
