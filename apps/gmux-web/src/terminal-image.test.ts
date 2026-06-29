import { describe, expect, test } from 'vitest'
import { imageAddonOptionsForTouchDevice, MOBILE_IMAGE_ADDON_OPTIONS, shouldLoadWebglRenderer } from './terminal-image'

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
})
