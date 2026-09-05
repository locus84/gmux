const STORAGE_PREFIX = 'gmux:checkout-fold:v1:'

type FoldStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

export function checkoutFoldStorageKey(folderKey: string, groupKey: string): string {
  return `${STORAGE_PREFIX}${encodeURIComponent(folderKey)}:${encodeURIComponent(groupKey)}`
}

/** Checkout groups default open; only collapsed groups occupy storage. */
export function readCheckoutExpanded(
  folderKey: string,
  groupKey: string,
  storage: FoldStorage | undefined = typeof localStorage === 'undefined' ? undefined : localStorage,
): boolean {
  if (!storage) return true
  try {
    return storage.getItem(checkoutFoldStorageKey(folderKey, groupKey)) !== 'collapsed'
  } catch {
    return true
  }
}

export function writeCheckoutExpanded(
  folderKey: string,
  groupKey: string,
  expanded: boolean,
  storage: FoldStorage | undefined = typeof localStorage === 'undefined' ? undefined : localStorage,
): void {
  if (!storage) return
  try {
    const key = checkoutFoldStorageKey(folderKey, groupKey)
    if (expanded) storage.removeItem(key)
    else storage.setItem(key, 'collapsed')
  } catch {
    // Persistence is best-effort; the in-memory fold still works.
  }
}
