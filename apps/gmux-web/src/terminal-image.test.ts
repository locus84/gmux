import { describe, expect, test } from 'vitest'
import { imageAddonOptionsForTouchDevice, MOBILE_IMAGE_ADDON_OPTIONS, shouldLoadWebglRenderer } from './terminal-image'

describe('mobile terminal image runtime', () => {
  test('keeps desktop image addon defaults by passing no override', () => {
    expect(imageAddonOptionsForTouchDevice(false)).toBeUndefined()
    expect(shouldLoadWebglRenderer(false)).toBe(true)
  })

  test('accepts common phone screenshots while bounding mobile image memory', () => {
    const commonPhoneScreenshotPixels = 1080 * 2340
    expect(MOBILE_IMAGE_ADDON_OPTIONS).toEqual({
      pixelLimit: 4_000_000,
      storageLimit: 16,
      showPlaceholder: false,
      sixelSizeLimit: 4_000_000,
      iipSizeLimit: 4_000_000,
      kittySizeLimit: 4_000_000,
    })
    expect(MOBILE_IMAGE_ADDON_OPTIONS.pixelLimit).toBeGreaterThan(commonPhoneScreenshotPixels)
    expect(MOBILE_IMAGE_ADDON_OPTIONS.storageLimit).toBeGreaterThanOrEqual(
      MOBILE_IMAGE_ADDON_OPTIONS.pixelLimit! * 4 / 1_000_000,
    )
    expect(imageAddonOptionsForTouchDevice(true)).toEqual(MOBILE_IMAGE_ADDON_OPTIONS)
    expect(shouldLoadWebglRenderer(true)).toBe(false)
  })

  test('returns a fresh options object for each addon instance', () => {
    const a = imageAddonOptionsForTouchDevice(true)
    const b = imageAddonOptionsForTouchDevice(true)
    expect(a).toEqual(b)
    expect(a).not.toBe(b)
  })
})
