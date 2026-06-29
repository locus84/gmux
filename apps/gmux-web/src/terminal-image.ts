import type { IImageAddonOptions } from '@xterm/addon-image'

const MOBILE_PI_CELL_HEIGHT_SCALE = 0.6
const MAX_REPORTED_CELL_PX = 512

type CellMetrics = {
  css?: {
    cell?: {
      width?: number
      height?: number
    }
  }
}

function boundedCellPx(value: number): number | null {
  if (!Number.isFinite(value) || value <= 0) return null
  return Math.max(1, Math.min(MAX_REPORTED_CELL_PX, Math.round(value)))
}

export function mobilePiImageCellSizeResponse(dimensions: CellMetrics | null | undefined): string | null {
  const cell = dimensions?.css?.cell
  const widthPx = boundedCellPx(cell?.width ?? 0)
  const heightPx = boundedCellPx((cell?.height ?? 0) * MOBILE_PI_CELL_HEIGHT_SCALE)
  if (widthPx == null || heightPx == null) return null
  // CSI 16 t response format: ESC [ 6 ; height ; width t.
  return `\x1b[6;${heightPx};${widthPx}t`
}

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
