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
