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
