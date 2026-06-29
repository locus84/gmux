import { describe, expect, test } from 'vitest'
import { imageAddonOptionsForTouchDevice, mobilePiImageCellSizeResponse, MOBILE_IMAGE_ADDON_OPTIONS, shouldLoadWebglRenderer } from './terminal-image'

describe('mobile terminal image runtime', () => {
  test('keeps desktop image addon defaults by passing no override', () => {
    expect(imageAddonOptionsForTouchDevice(false)).toBeUndefined()
    expect(shouldLoadWebglRenderer(false)).toBe(true)
  })

  test('uses bounded image addon options and disables WebGL on touch devices', () => {
    expect(imageAddonOptionsForTouchDevice(true)).toEqual(MOBILE_IMAGE_ADDON_OPTIONS)
    expect(shouldLoadWebglRenderer(true)).toBe(false)
  })

  test('returns a fresh options object for each addon instance', () => {
    const a = imageAddonOptionsForTouchDevice(true)
    const b = imageAddonOptionsForTouchDevice(true)
    expect(a).toEqual(b)
    expect(a).not.toBe(b)
  })

  test('builds a mobile Pi cell-size response with reduced row height', () => {
    expect(mobilePiImageCellSizeResponse({
      css: { cell: { width: 10.4, height: 17 } },
    })).toBe('\x1b[6;10;10t')
  })

  test('ignores missing or invalid mobile Pi cell metrics', () => {
    expect(mobilePiImageCellSizeResponse(null)).toBeNull()
    expect(mobilePiImageCellSizeResponse({ css: { cell: { width: 0, height: 17 } } })).toBeNull()
    expect(mobilePiImageCellSizeResponse({ css: { cell: { width: 10, height: Number.NaN } } })).toBeNull()
  })
})
