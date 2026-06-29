import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createTouchInlineImageSanitizer, shouldUseTouchInlineImageDecodeFallback } from './xterm-image-compat'

if (typeof globalThis.window === 'undefined') {
  ;(globalThis as any).window = globalThis
}

const matchMediaMock = vi.fn().mockImplementation((query: string) => ({
  matches: query === '(pointer: coarse)',
  media: query,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
  addListener: vi.fn(),
  removeListener: vi.fn(),
  onchange: null,
  dispatchEvent: vi.fn(),
}))

Object.defineProperty(window, 'matchMedia', { value: matchMediaMock, writable: true, configurable: true })
function setNavigator(value: { maxTouchPoints: number; platform: string; userAgent: string }): void {
  Object.defineProperty(globalThis, 'navigator', {
    value: { ...(globalThis.navigator ?? {}), ...value },
    configurable: true,
  })
}

function setIpadNavigator(): void {
  setNavigator({
    maxTouchPoints: 5,
    platform: 'MacIntel',
    userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
  })
}

function setAndroidEdgeNavigator(): void {
  setNavigator({
    maxTouchPoints: 5,
    platform: 'Linux armv81',
    userAgent: 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36 EdgA/125.0.0.0',
  })
}

setIpadNavigator()

function fakeTerm(cols = 80, rows = 40) {
  return {
    cols,
    rows,
    dimensions: { css: { cell: { width: 10, height: 20 } } },
    buffer: { active: { cursorX: 0 } },
  } as any
}

function pngBase64(width: number, height: number): string {
  const bytes = new Uint8Array(32)
  bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0)
  bytes.set([0x00, 0x00, 0x00, 0x0d], 8)
  bytes.set([0x49, 0x48, 0x44, 0x52], 12)
  writeUint32BE(bytes, 16, width)
  writeUint32BE(bytes, 20, height)
  let raw = ''
  for (const byte of bytes) raw += String.fromCharCode(byte)
  return btoa(raw)
}

function writeUint32BE(bytes: Uint8Array, offset: number, value: number): void {
  bytes[offset] = (value >>> 24) & 0xff
  bytes[offset + 1] = (value >>> 16) & 0xff
  bytes[offset + 2] = (value >>> 8) & 0xff
  bytes[offset + 3] = value & 0xff
}

function bytes(value: string): Uint8Array {
  const out = new Uint8Array(value.length)
  for (let i = 0; i < value.length; i++) out[i] = value.charCodeAt(i) & 0xff
  return out
}

function text(chunks: Uint8Array[]): string {
  return chunks.map(chunk => String.fromCharCode(...chunk)).join('')
}

describe('shouldUseTouchInlineImageDecodeFallback', () => {
  beforeEach(() => {
    matchMediaMock.mockClear()
  })

  it('uses the canvas decode fallback on Android Edge Chromium touch', () => {
    setAndroidEdgeNavigator()

    expect(shouldUseTouchInlineImageDecodeFallback()).toBe(true)
  })

  it('uses the canvas decode fallback on iPad WebKit', () => {
    setIpadNavigator()

    expect(shouldUseTouchInlineImageDecodeFallback()).toBe(true)
  })
})

describe('createTouchInlineImageSanitizer', () => {
  beforeEach(() => {
    matchMediaMock.mockClear()
    setIpadNavigator()
  })

  it('caps tall iTerm inline images by terminal rows on touch devices', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const payload = `before\x1b]1337;File=inline=1;size=123:${pngBase64(720, 1280)}\x07after`

    const rewritten = text(sanitizer.transform(bytes(payload)))

    expect(rewritten).toContain('\x1b]1337;File=inline=1;size=123;height=18;preserveAspectRatio=1:')
    expect(rewritten).toContain('after')
  })

  it('caps wide iTerm inline images by available terminal columns', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const payload = `\x1b]1337;File=inline=1:${pngBase64(1280, 320)}\x07`

    const rewritten = text(sanitizer.transform(bytes(payload)))

    expect(rewritten).toContain('\x1b]1337;File=inline=1;size=32;width=80;preserveAspectRatio=1:')
  })

  it('waits for a split inline image header before rewriting', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))

    expect(sanitizer.transform(bytes('\x1b]1337;Fi'))).toEqual([])
    const rewritten = text(sanitizer.transform(bytes(`le=inline=1:${pngBase64(720, 1280)}\x07`)))

    expect(rewritten).toContain('height=18;preserveAspectRatio=1')
  })

  it('converts kitty graphics output to iTerm inline images on WebKit touch devices', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const image = pngBase64(720, 1280)
    const payload = `before\x1b_Ga=T,f=100,q=2,C=1,c=49,r=18;${image}\x1b\\after`

    const rewritten = text(sanitizer.transform(bytes(payload)))

    expect(rewritten).toContain('before')
    expect(rewritten).toContain('\x1b]1337;File=inline=1;size=32;width=49;height=18;preserveAspectRatio=0:')
    expect(rewritten).toContain(image)
    expect(rewritten).toContain('\x07\x1b[17Aafter')
    expect(rewritten).not.toContain('\x1b_G')
  })

  it('does not restore the cursor for converted kitty graphics when C=1 is absent', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const image = pngBase64(720, 1280)

    const rewritten = text(sanitizer.transform(bytes(`\x1b_Ga=T,f=100,q=2,c=49,r=18;${image}\x1b\\after`)))

    expect(rewritten).toContain('\x07after')
    expect(rewritten).not.toContain('\x1b[17A')
  })

  it('converts kitty graphics on Android Edge while using the canvas decode fallback', () => {
    setAndroidEdgeNavigator()
    expect(shouldUseTouchInlineImageDecodeFallback()).toBe(true)
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const image = pngBase64(720, 1280)
    const payload = `before\x1b_Ga=T,f=100,q=2,C=1,c=49,r=18;${image}\x1b\\after`

    const rewritten = text(sanitizer.transform(bytes(payload)))

    expect(rewritten).toContain('\x1b]1337;File=inline=1;size=32;width=49;height=18;preserveAspectRatio=0:')
    expect(rewritten).toContain('\x07\x1b[17Aafter')
    expect(rewritten).not.toContain('\x1b_G')
  })

  it('clamps oversized kitty cell placements to the mobile viewport', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const image = pngBase64(720, 1280)
    const payload = `\x1b_Ga=T,f=100,q=2,C=1,c=200,r=60;${image}\x1b\\after`

    const rewritten = text(sanitizer.transform(bytes(payload)))

    expect(rewritten).toContain('\x1b]1337;File=inline=1;size=32;width=80;height=18;preserveAspectRatio=0:')
    expect(rewritten).toContain('\x07\x1b[17Aafter')
  })

  it('joins chunked kitty graphics payloads before converting on WebKit touch devices', () => {
    const sanitizer = createTouchInlineImageSanitizer(fakeTerm(80, 40))
    const image = pngBase64(1280, 320)
    const first = image.slice(0, 20)
    const second = image.slice(20)

    const firstOut = text(sanitizer.transform(bytes(`\x1b_Ga=T,f=100,m=1,c=40;${first}\x1b\\`)))
    const secondOut = text(sanitizer.transform(bytes(`\x1b_Gm=0;${second}\x1b\\`)))

    expect(firstOut).toBe('')
    expect(secondOut).toContain('\x1b]1337;File=inline=1;size=32;width=40;preserveAspectRatio=1:')
    expect(secondOut).toContain(image)
  })
})
