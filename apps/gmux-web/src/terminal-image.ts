import type { IImageAddonOptions } from '@xterm/addon-image'

export const MOBILE_IMAGE_ADDON_OPTIONS: Readonly<IImageAddonOptions> = Object.freeze({
  // Mobile WebKit/Chromium memory pressure is much harsher than desktop.
  // Keep decode/storage bounded so a failed image does not leave the terminal
  // fighting huge canvas buffers. Desktop keeps xterm-addon-image defaults.
  pixelLimit: 1_000_000,
  storageLimit: 8,
  showPlaceholder: false,
  sixelSizeLimit: 4_000_000,
  iipSizeLimit: 4_000_000,
  kittySizeLimit: 4_000_000,
})

export function imageAddonOptionsForTouchDevice(touchDevice: boolean): IImageAddonOptions | undefined {
  return touchDevice ? { ...MOBILE_IMAGE_ADDON_OPTIONS } : undefined
}

export function shouldLoadWebglRenderer(touchDevice: boolean): boolean {
  return !touchDevice
}

const IMAGE_CANVAS_DPR_CORRECTION_VAR = '--gmux-image-canvas-dpr-correction'
let imageCanvasPatchInstalled = false

function imageLayerCanvasScale(canvas: HTMLCanvasElement): number {
  if (!canvas.classList.contains('xterm-image-layer-top') && !canvas.classList.contains('xterm-image-layer-bottom')) {
    return 1
  }
  const container = canvas.closest<HTMLElement>('.terminal-container')
  if (!container) return 1
  if (getComputedStyle(container).getPropertyValue(IMAGE_CANVAS_DPR_CORRECTION_VAR).trim() !== '1') return 1

  const cssWidth = parseFloat(canvas.style.width || '') || canvas.getBoundingClientRect().width
  if (!Number.isFinite(cssWidth) || cssWidth <= 0) return 1
  const scale = canvas.width / cssWidth
  return Number.isFinite(scale) && scale > 1 ? scale : 1
}

export function installImageCanvasDprCorrection(): void {
  if (imageCanvasPatchInstalled || typeof CanvasRenderingContext2D === 'undefined') return
  imageCanvasPatchInstalled = true

  // xterm-addon-image sizes its image layer canvas in device pixels while its
  // draw calls use CSS cell coordinates. On high-DPR mobile screens that makes
  // inline images render at 1 / DPR size. Correct only gmux mobile image-layer
  // canvases by scaling destination coordinates into the backing store; unlike
  // CSS transform this preserves terminal row/cell positioning.
  const originalDrawImage = CanvasRenderingContext2D.prototype.drawImage
  CanvasRenderingContext2D.prototype.drawImage = function drawImageWithImageLayerDprCorrection(
    this: CanvasRenderingContext2D,
    image: CanvasImageSource,
    ...args: [number, number] | [number, number, number, number] | [number, number, number, number, number, number, number, number]
  ): void {
    const scale = imageLayerCanvasScale(this.canvas)
    let correctedArgs: typeof args = args
    if (scale > 1) {
      if (args.length === 2) {
        correctedArgs = [args[0] * scale, args[1] * scale]
      } else if (args.length === 4) {
        correctedArgs = [args[0] * scale, args[1] * scale, args[2] * scale, args[3] * scale]
      } else if (args.length === 8) {
        correctedArgs = [args[0], args[1], args[2], args[3], args[4] * scale, args[5] * scale, args[6] * scale, args[7] * scale]
      }
    }
    Reflect.apply(originalDrawImage, this, [image, ...correctedArgs])
  } as typeof CanvasRenderingContext2D.prototype.drawImage

  const originalClearRect = CanvasRenderingContext2D.prototype.clearRect
  CanvasRenderingContext2D.prototype.clearRect = function clearRectWithImageLayerDprCorrection(
    this: CanvasRenderingContext2D,
    x: number,
    y: number,
    w: number,
    h: number,
  ): void {
    const scale = imageLayerCanvasScale(this.canvas)
    if (scale > 1) return originalClearRect.call(this, x * scale, y * scale, w * scale, h * scale)
    return originalClearRect.call(this, x, y, w, h)
  }
}

export function configureImageCanvasDprCorrection(host: HTMLElement, touchDevice: boolean): void {
  installImageCanvasDprCorrection()
  if (touchDevice) {
    host.style.setProperty(IMAGE_CANVAS_DPR_CORRECTION_VAR, '1')
  } else {
    host.style.removeProperty(IMAGE_CANVAS_DPR_CORRECTION_VAR)
  }
}
