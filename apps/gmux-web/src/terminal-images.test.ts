import { describe, expect, it, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import type { TerminalImageAsset } from './terminal-images'
import { installTerminalImages, parseTerminalImageOsc, terminalImageIntersectsErase, terminalImageUrl } from './terminal-images'
import { fetchTerminalImage } from './terminal-image-viewer'

const asset: TerminalImageAsset = {
  version: 1,
  action: 'put',
  hash: 'a'.repeat(64),
  id: 7,
  cols: 20,
  rows: 8,
  bytes: 24,
  mime: 'image/png',
}

function pngHeader(width = 100, height = 50): Uint8Array {
  const bytes = new Uint8Array(24)
  bytes.set([137, 80, 78, 71, 13, 10, 26, 10])
  bytes.set([73, 72, 68, 82], 12)
  const view = new DataView(bytes.buffer)
  view.setUint32(16, width)
  view.setUint32(20, height)
  return bytes
}

describe('terminal image reference protocol', () => {
  it('accepts the narrow put/delete schema', () => {
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify(asset)}`)?.success).toBe(true)
    expect(parseTerminalImageOsc('gmux-image;{"version":1,"action":"delete","id":7}')?.success).toBe(true)
    expect(parseTerminalImageOsc('gmux-image;{"version":1,"action":"delete","all":true}')?.success).toBe(true)
  })

  it('rejects untrusted hashes, bounds, extra keys, and oversized OSC data', () => {
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify({ ...asset, hash: '../image' })}`)?.success).toBe(false)
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify({ ...asset, bytes: 8 * 1024 * 1024 + 1 })}`)?.success).toBe(false)
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify({ ...asset, id: 0 })}`)?.success).toBe(false)
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify({ ...asset, cols: 1001 })}`)?.success).toBe(false)
    expect(parseTerminalImageOsc(`gmux-image;${JSON.stringify({ ...asset, surprise: true })}`)?.success).toBe(false)
    expect(parseTerminalImageOsc(`gmux-image;${'x'.repeat(1025)}`)).toBeNull()
    expect(parseTerminalImageOsc('other;{}')).toBeNull()
  })

  it('identifies only placements touched by ED/EL ranges', () => {
    const image = { x: 5, row: 10, cols: 4, rows: 3 }
    expect(terminalImageIntersectsErase(image, 10, 9, 10, 20)).toBe(false)
    expect(terminalImageIntersectsErase(image, 11, 7, 11, 20)).toBe(true)
    expect(terminalImageIntersectsErase(image, 0, 0, 9, 100)).toBe(false)
    expect(terminalImageIntersectsErase(image, 12, 0, 20, 100)).toBe(true)
  })

  it('matches cell-by-cell erasure for partial ED boundaries and multi-row images', () => {
    for (let row = 0; row < 4; row++) for (let x = 0; x < 4; x++) {
      const image = { x, row, cols: 2, rows: 2 }
      for (let fromRow = 0; fromRow < 5; fromRow++) for (let toRow = fromRow; toRow < 5; toRow++) {
        for (let fromCol = 0; fromCol < 5; fromCol++) for (let toCol = 0; toCol < 5; toCol++) {
          if (fromRow === toRow && fromCol > toCol) continue
          let expected = false
          for (let y = row; y < row + 2; y++) for (let col = x; col < x + 2; col++) {
            if ((y > fromRow || (y === fromRow && col >= fromCol))
                && (y < toRow || (y === toRow && col <= toCol))) expected = true
          }
          expect(terminalImageIntersectsErase(image, fromRow, fromCol, toRow, toCol)).toBe(expected)
        }
      }
    }
  })

  it('builds only the session-scoped content-addressed route', () => {
    expect(terminalImageUrl('peer/session', 'a'.repeat(64))).toBe(`/v1/sessions/peer%2Fsession/images/${'a'.repeat(64)}`)
  })
})

describe('terminal image controller lifecycle', () => {
  it('retains parse-time ownership and original buffer semantics after moving to the tray', () => {
    class FakeElement {
      hidden = false
      parentElement: FakeElement | null = null
      children: FakeElement[] = []
      className = ''
      title = ''
      innerHTML = ''
      type = ''
      classList = { add: vi.fn() }
      listeners = new Map<string, (event: { preventDefault(): void; stopPropagation(): void }) => void>()
      setAttribute() {}
      addEventListener(name: string, listener: (event: { preventDefault(): void; stopPropagation(): void }) => void) { this.listeners.set(name, listener) }
      appendChild(child: FakeElement) { child.remove(); child.parentElement = this; this.children.push(child); return child }
      replaceChildren(...children: FakeElement[]) { for (const child of this.children) child.parentElement = null; this.children = []; for (const child of children) this.appendChild(child) }
      remove() { if (!this.parentElement) return; this.parentElement.children = this.parentElement.children.filter(child => child !== this); this.parentElement = null }
      click() { this.listeners.get('click')?.({ preventDefault() {}, stopPropagation() {} }) }
    }
    vi.stubGlobal('document', { createElement: () => new FakeElement() })
    const oscHandlers = new Map<number, (data: string) => boolean>()
    const csiHandlers = new Map<string, (params: (number | number[])[]) => boolean>()
    let resizeHandler: ((size: { cols: number; rows: number }) => void) | undefined
    let bufferHandler: ((buffer: { type: 'normal' | 'alternate' }) => void) | undefined
    const active = { type: 'normal' as 'normal' | 'alternate', cursorX: 0, cursorY: 0, baseY: 0 }
    const disposable = { dispose() {} }
    const term = {
      cols: 80,
      rows: 24,
      dimensions: { css: { cell: { width: 8, height: 16 } } },
      buffer: {
        active,
        onBufferChange(listener: typeof bufferHandler) { bufferHandler = listener; return disposable },
      },
      parser: {
        registerOscHandler(id: number, handler: (data: string) => boolean) { oscHandlers.set(id, handler); return disposable },
        registerCsiHandler(id: { final: string }, handler: (params: (number | number[])[]) => boolean) { csiHandlers.set(id.final, handler); return disposable },
        registerEscHandler() { return disposable },
      },
      registerMarker() {
        const listeners: Array<() => void> = []
        return { line: active.baseY + active.cursorY, isDisposed: false, id: 1, onDispose(listener: () => void) { listeners.push(listener); return { dispose() { const index = listeners.indexOf(listener); if (index >= 0) listeners.splice(index, 1) } } }, dispose() { for (const listener of [...listeners]) listener() } }
      },
      registerDecoration() {
        const host = new FakeElement()
        return { marker: {}, element: host, isDisposed: false, onDispose: () => disposable, onRender(listener: (element: FakeElement) => void) { listener(host); return disposable }, dispose() {} }
      },
      onResize(listener: typeof resizeHandler) { resizeHandler = listener; return disposable },
    } as unknown as Terminal
    const tray = new FakeElement()
    let owner = 'old-session'
    let activatedOwner = ''
    const controller = installTerminalImages(term, tray as unknown as HTMLElement, () => {
      const captured = owner
      return () => { activatedOwner = captured }
    })
    const send = (message: object) => oscHandlers.get(777)!(`gmux-image;${JSON.stringify(message)}`)

    send(asset)
    owner = 'new-session'
    resizeHandler!({ cols: 79, rows: 24 })
    expect(tray.children).toHaveLength(1)
    tray.children[0].click()
    expect(activatedOwner).toBe('old-session')
    csiHandlers.get('J')!([2])
    expect(tray.children).toHaveLength(0)

    send(asset)
    resizeHandler!({ cols: 78, rows: 24 })
    active.type = 'alternate'; bufferHandler!(active)
    send({ ...asset, id: 43 })
    expect(tray.children.filter(button => !button.hidden)).toHaveLength(1)
    csiHandlers.get('J')!([2])
    expect(tray.children.filter(button => !button.hidden)).toHaveLength(0)
    active.type = 'normal'; bufferHandler!(active)
    expect(tray.children.filter(button => !button.hidden)).toHaveLength(1)
    send({ version: 1, action: 'delete', id: asset.id })
    expect(tray.children).toHaveLength(0)
    controller.dispose()
    vi.unstubAllGlobals()
  })
})

describe('terminal image fetch', () => {
  it('fetches and validates PNG dimensions only after explicitly invoked', async () => {
    const fetcher = vi.fn(async () => new Response(Uint8Array.from(pngHeader()).buffer, {
      status: 200,
      headers: { 'content-length': '24', 'content-type': 'image/png' },
    })) as unknown as typeof fetch
    expect(fetcher).not.toHaveBeenCalled()
    const result = await fetchTerminalImage('s1', asset, new AbortController().signal, fetcher)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(result).toMatchObject({ width: 100, height: 50 })
  })

  it('accepts a valid chunked response without Content-Length', async () => {
    const fetcher = vi.fn(async () => new Response(Uint8Array.from(pngHeader()).buffer, {
      status: 200,
      headers: { 'content-type': 'image/png' },
    })) as unknown as typeof fetch
    await expect(fetchTerminalImage('s1', asset, new AbortController().signal, fetcher)).resolves.toMatchObject({ width: 100, height: 50 })
  })

  it('surfaces typed expiry errors and rejects oversized dimensions', async () => {
    const expired = vi.fn(async () => new Response(JSON.stringify({ error: { code: 'image_expired' } }), {
      status: 404,
      headers: { 'content-type': 'application/json' },
    })) as unknown as typeof fetch
    await expect(fetchTerminalImage('s1', asset, new AbortController().signal, expired)).rejects.toMatchObject({ code: 'image_expired' })

    const missing = vi.fn(async () => new Response(JSON.stringify({ error: { code: 'image_not_found' } }), {
      status: 404,
      headers: { 'content-type': 'application/json' },
    })) as unknown as typeof fetch
    await expect(fetchTerminalImage('s1', asset, new AbortController().signal, missing)).rejects.toMatchObject({ code: 'image_not_found' })

    const huge = vi.fn(async () => new Response(Uint8Array.from(pngHeader(8193, 1)).buffer, {
      status: 200,
      headers: { 'content-length': '24', 'content-type': 'image/png' },
    })) as unknown as typeof fetch
    await expect(fetchTerminalImage('s1', asset, new AbortController().signal, huge)).rejects.toMatchObject({ code: 'invalid_image' })

    const tooManyPixels = vi.fn(async () => new Response(Uint8Array.from(pngHeader(4096, 4096)).buffer, {
      status: 200,
      headers: { 'content-length': '24', 'content-type': 'image/png' },
    })) as unknown as typeof fetch
    await expect(fetchTerminalImage('s1', asset, new AbortController().signal, tooManyPixels)).rejects.toMatchObject({ code: 'invalid_image' })
  })
})
