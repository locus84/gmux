import { describe, expect, it } from 'vitest'
import { checkoutFoldStorageKey, readCheckoutExpanded, writeCheckoutExpanded } from './checkout-fold'

function memoryStorage(): Storage {
  const values = new Map<string, string>()
  return {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: key => values.get(key) ?? null,
    key: index => [...values.keys()][index] ?? null,
    removeItem: key => { values.delete(key) },
    setItem: (key, value) => { values.set(key, value) },
  }
}

describe('checkout fold persistence', () => {
  it('defaults groups open and stores only collapsed groups', () => {
    const storage = memoryStorage()
    expect(readCheckoutExpanded('::gmux', '/repo', storage)).toBe(true)
    writeCheckoutExpanded('::gmux', '/repo', false, storage)
    expect(readCheckoutExpanded('::gmux', '/repo', storage)).toBe(false)
    writeCheckoutExpanded('::gmux', '/repo', true, storage)
    expect(storage.getItem(checkoutFoldStorageKey('::gmux', '/repo'))).toBeNull()
  })

  it('namespaces groups by project and checkout', () => {
    expect(checkoutFoldStorageKey('peer::gmux', '/repo')).not.toBe(checkoutFoldStorageKey('::gmux', '/repo'))
    expect(checkoutFoldStorageKey('::gmux', '/repo/a')).not.toBe(checkoutFoldStorageKey('::gmux', '/repo/b'))
  })
})
