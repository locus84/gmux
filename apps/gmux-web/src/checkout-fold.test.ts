import { describe, expect, it } from 'vitest'
import { checkoutFoldStorageKey, readCheckoutExpanded, writeCheckoutExpanded } from './checkout-fold'

function memoryStorage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
    removeItem: (key: string) => { values.delete(key) },
  }
}

describe('checkout fold persistence', () => {
  it('defaults new checkout groups to expanded', () => {
    expect(readCheckoutExpanded('::gmux', 'checkout:/repo', memoryStorage())).toBe(true)
  })

  it('remembers collapsed groups independently', () => {
    const storage = memoryStorage()
    writeCheckoutExpanded('::gmux', 'checkout:/repo/a', false, storage)

    expect(readCheckoutExpanded('::gmux', 'checkout:/repo/a', storage)).toBe(false)
    expect(readCheckoutExpanded('::gmux', 'checkout:/repo/b', storage)).toBe(true)
    expect(readCheckoutExpanded('peer::gmux', 'checkout:/repo/a', storage)).toBe(true)
  })

  it('removes the cached override when expanded again', () => {
    const storage = memoryStorage()
    writeCheckoutExpanded('::gmux', 'checkout:/repo', false, storage)
    writeCheckoutExpanded('::gmux', 'checkout:/repo', true, storage)
    expect(readCheckoutExpanded('::gmux', 'checkout:/repo', storage)).toBe(true)
  })

  it('falls back to expanded when storage is unavailable', () => {
    const broken = {
      getItem: () => { throw new Error('blocked') },
      setItem: () => { throw new Error('blocked') },
      removeItem: () => { throw new Error('blocked') },
    }
    expect(readCheckoutExpanded('::gmux', '/repo', broken)).toBe(true)
    expect(() => writeCheckoutExpanded('::gmux', '/repo', false, broken)).not.toThrow()
  })

  it('encodes project and checkout identities in the key', () => {
    expect(checkoutFoldStorageKey('peer::my project', 'checkout:~/a/b')).not.toContain(' ')
  })
})
