import { TerminalImageMessageSchema, type TerminalImagePut } from '@gmux/protocol'
import type { IDecoration, IDisposable, IMarker, Terminal } from '@xterm/xterm'

export const TERMINAL_IMAGE_OSC = 777
export const TERMINAL_IMAGE_PREFIX = 'gmux-image;'
export const TERMINAL_IMAGE_MAX_OSC_CHARS = 1024

export interface TerminalImageAsset extends TerminalImagePut {}

export function terminalImageUrl(sessionId: string, hash: string): string {
  return `/v1/sessions/${encodeURIComponent(sessionId)}/images/${hash}`
}

export function parseTerminalImageOsc(data: string): ReturnType<typeof TerminalImageMessageSchema.safeParse> | null {
  if (!data.startsWith(TERMINAL_IMAGE_PREFIX)) return null
  const json = data.slice(TERMINAL_IMAGE_PREFIX.length)
  if (json.length === 0 || json.length > TERMINAL_IMAGE_MAX_OSC_CHARS) return null
  try {
    return TerminalImageMessageSchema.safeParse(JSON.parse(json))
  } catch {
    return null
  }
}

interface Placement {
  asset: TerminalImageAsset
  originBuffer: 'normal' | 'alternate'
  presentation: 'grid' | 'tray'
  activate: () => void
  x: number
  /** Absolute normal-buffer row or viewport-relative alternate-buffer row. */
  row: number
  marker?: IMarker
  decoration?: IDecoration
  button?: HTMLButtonElement
  anchorDisposables: IDisposable[]
}

export interface TerminalImageController extends IDisposable {
  readonly installed: true
  clear(): void
}

export function terminalImageIntersectsErase(
  placement: { x: number; row: number; cols: number; rows: number },
  fromRow: number,
  fromCol: number,
  toRow: number,
  toCol: number,
): boolean {
  const pBottom = placement.row + placement.rows - 1
  const pRight = placement.x + placement.cols - 1
  const first = Math.max(placement.row, fromRow)
  const last = Math.min(pBottom, toRow)
  if (first > last) return false
  // Only the first/last erased rows have horizontal bounds. An image may
  // overlap just one of those rows even when the image itself spans many.
  const intersectsRow = (row: number) => pRight >= (row === fromRow ? fromCol : 0)
    && placement.x <= (row === toRow ? toCol : Number.MAX_SAFE_INTEGER)
  return last - first > 1 || intersectsRow(first) || intersectsRow(last)
}

/**
 * Installs the synchronous OSC parser and public xterm decorations.
 *
 * xterm's public markers deliberately support only the normal buffer. In the
 * alternate buffer placements are exposed in the supplied session tray rather
 * than guessed at screen coordinates; this keeps them loadable without ever
 * presenting a stale/misplaced in-grid control.
 */
export function installTerminalImages(
  term: Terminal,
  tray: HTMLElement,
  prepareActivation: (asset: TerminalImageAsset) => () => void,
): TerminalImageController {
  const placements = new Set<Placement>()
  const disposables: IDisposable[] = []
  const maxPlacements = 128
  let previousCols = term.cols

  const remove = (placement: Placement) => {
    if (!placements.delete(placement)) return
    for (const disposable of placement.anchorDisposables.splice(0)) disposable.dispose()
    placement.button?.remove()
    placement.decoration?.dispose()
    placement.marker?.dispose()
  }
  const removeWhere = (predicate: (placement: Placement) => boolean) => {
    for (const placement of [...placements]) if (predicate(placement)) remove(placement)
  }
  const label = (asset: TerminalImageAsset) => `Open PNG image, ${asset.cols} by ${asset.rows} terminal cells, ${asset.bytes.toLocaleString()} bytes`
  const makeButton = (placement: Placement, inTray: boolean) => {
    const { asset } = placement
    const button = document.createElement('button')
    button.type = 'button'
    button.className = inTray ? 'terminal-image-button terminal-image-tray-button' : 'terminal-image-button'
    button.setAttribute('aria-label', label(asset))
    button.title = label(asset)
    button.innerHTML = `<span aria-hidden="true">▧</span><span>${inTray ? 'Image' : 'PNG'} · ${asset.bytes < 1024 ? `${asset.bytes} B` : `${(asset.bytes / 1024).toFixed(asset.bytes >= 10240 ? 0 : 1)} KB`}</span>`
    // Keep xterm's capture-phase touch/link handlers from treating the button
    // as a terminal gesture. Focus remains on the button for keyboard users.
    for (const eventName of ['pointerdown', 'touchstart', 'touchmove', 'touchend']) {
      button.addEventListener(eventName, event => event.stopPropagation())
    }
    button.addEventListener('click', event => {
      event.preventDefault()
      event.stopPropagation()
      placement.activate()
    })
    return button
  }
  const refreshTrayVisibility = () => {
    const activeBuffer = term.buffer.active.type
    let visible = false
    for (const placement of placements) {
      if (placement.presentation !== 'tray' || !placement.button) continue
      placement.button.hidden = placement.originBuffer !== activeBuffer
      if (!placement.button.hidden) visible = true
    }
    tray.hidden = !visible
  }
  const moveToTray = (placement: Placement) => {
    // Preserve the marker's current absolute row before dropping the grid
    // anchor; ED/EL still apply according to the placement's origin buffer.
    if (placement.marker && placement.marker.line >= 0) placement.row = placement.marker.line
    for (const disposable of placement.anchorDisposables.splice(0)) disposable.dispose()
    placement.button?.remove()
    placement.decoration?.dispose()
    placement.marker?.dispose()
    placement.decoration = undefined
    placement.marker = undefined
    placement.presentation = 'tray'
    placement.button = makeButton(placement, true)
    tray.appendChild(placement.button)
  }

  const put = (asset: TerminalImageAsset) => {
    // v1 has image IDs but no placement IDs, so a put replaces the prior
    // placement for that ID. This exactly matches the Pi C=1 subset.
    removeWhere(placement => placement.asset.id === asset.id)
    const buffer = term.buffer.active
    const placement: Placement = {
      asset,
      originBuffer: buffer.type,
      presentation: buffer.type === 'normal' ? 'grid' : 'tray',
      activate: prepareActivation(asset),
      x: Math.min(buffer.cursorX, Math.max(0, term.cols - 1)),
      row: buffer.type === 'normal' ? buffer.baseY + buffer.cursorY : buffer.cursorY,
      anchorDisposables: [],
    }
    placements.add(placement)
    while (placements.size > maxPlacements) remove(placements.values().next().value!)

    if (buffer.type === 'normal') {
      const cell = term.dimensions?.css.cell
      // Never squeeze the only action into a target smaller than 44px. Small
      // placements remain available in the touch-safe session tray.
      if (!cell || asset.cols * cell.width < 44 || asset.rows * cell.height < 44) {
        moveToTray(placement)
        refreshTrayVisibility()
        return
      }
      const marker = term.registerMarker(0)
      const decoration = marker && term.registerDecoration({
        marker,
        x: placement.x,
        width: Math.min(asset.cols, Math.max(1, term.cols - placement.x)),
        height: Math.min(asset.rows, term.rows),
        layer: 'top',
      })
      if (!marker || !decoration) {
        marker?.dispose()
        moveToTray(placement)
        refreshTrayVisibility()
        return
      }
      placement.marker = marker
      placement.decoration = decoration
      placement.anchorDisposables.push(marker.onDispose(() => remove(placement)))
      placement.anchorDisposables.push(decoration.onRender(element => {
        if (!placement.button) placement.button = makeButton(placement, false)
        if (placement.button.parentElement !== element) element.replaceChildren(placement.button)
        element.classList.add('terminal-image-decoration')
      }))
    } else {
      placement.button = makeButton(placement, true)
      tray.appendChild(placement.button)
    }
    refreshTrayVisibility()
  }

  disposables.push(term.parser.registerOscHandler(TERMINAL_IMAGE_OSC, data => {
    const parsed = parseTerminalImageOsc(data)
    // The namespace belongs to gmux. Malformed gmux payloads are consumed so
    // they cannot become visible terminal text or reach another OSC handler.
    if (!parsed) return data.startsWith(TERMINAL_IMAGE_PREFIX)
    if (!parsed.success) return true
    const message = parsed.data
    if (message.action === 'put') put(message)
    else if ('all' in message) removeWhere(() => true)
    else removeWhere(placement => placement.asset.id === message.id)
    refreshTrayVisibility()
    return true
  }))

  const eraseDisplay = (params: (number | number[])[]) => {
    const mode = typeof params[0] === 'number' ? params[0] : 0
    const buffer = term.buffer.active
    const top = buffer.type === 'normal' ? buffer.baseY : 0
    const bottom = top + term.rows - 1
    const cursorRow = top + buffer.cursorY
    const cursorCol = buffer.cursorX
    removeWhere(placement => {
      if (placement.originBuffer !== buffer.type) return false
      const row = placement.marker?.line ?? placement.row
      const box = { x: placement.x, row, cols: placement.asset.cols, rows: placement.asset.rows }
      // ED 2 erases only the visible screen. ED 3 erases scrollback while
      // retaining the visible screen; neither may indiscriminately drop both.
      if (mode === 2) return terminalImageIntersectsErase(box, top, 0, bottom, Number.MAX_SAFE_INTEGER)
      if (mode === 3) return buffer.type === 'normal' && top > 0
        && terminalImageIntersectsErase(box, 0, 0, top - 1, Number.MAX_SAFE_INTEGER)
      if (mode === 1) return terminalImageIntersectsErase(box, top, 0, cursorRow, cursorCol)
      return terminalImageIntersectsErase(box, cursorRow, cursorCol, bottom, Number.MAX_SAFE_INTEGER)
    })
    return false
  }
  const eraseLine = (params: (number | number[])[]) => {
    const mode = typeof params[0] === 'number' ? params[0] : 0
    const buffer = term.buffer.active
    const cursorRow = buffer.type === 'normal' ? buffer.baseY + buffer.cursorY : buffer.cursorY
    const from = mode === 1 || mode === 2 ? 0 : buffer.cursorX
    const to = mode === 0 || mode === 2 ? Number.MAX_SAFE_INTEGER : buffer.cursorX
    removeWhere(placement => placement.originBuffer === buffer.type && terminalImageIntersectsErase({
      x: placement.x,
      row: placement.marker?.line ?? placement.row,
      cols: placement.asset.cols,
      rows: placement.asset.rows,
    }, cursorRow, from, cursorRow, to))
    return false
  }
  disposables.push(term.parser.registerCsiHandler({ final: 'J' }, eraseDisplay))
  disposables.push(term.parser.registerCsiHandler({ final: 'K' }, eraseLine))
  disposables.push(term.parser.registerEscHandler({ final: 'c' }, () => {
    removeWhere(() => true)
    refreshTrayVisibility()
    return false
  }))
  disposables.push(term.buffer.onBufferChange(buffer => {
    // Alternate buffers have no public markers/decorations. A newly activated
    // alternate buffer is blank under the modes used by full-screen TUIs;
    // discard old tray entries and let replay/redraw repopulate it.
    if (buffer.type === 'alternate') removeWhere(placement => placement.originBuffer === 'alternate')
    refreshTrayVisibility()
  }))
  disposables.push(term.onResize(({ cols }) => {
    if (cols !== previousCols) {
      // Marker lines survive scrollback, but public markers do not track the
      // column movement caused by reflow. Preserve access in the explicitly
      // labelled tray rather than leave a plausible-but-wrong grid control or
      // discard an image ID that Pi may not retransmit.
      for (const placement of [...placements]) {
        if (placement.presentation === 'grid') moveToTray(placement)
      }
      previousCols = cols
    }
    refreshTrayVisibility()
  }))

  return {
    installed: true,
    clear() {
      removeWhere(() => true)
      refreshTrayVisibility()
    },
    dispose() {
      removeWhere(() => true)
      for (const disposable of disposables.splice(0)) disposable.dispose()
      tray.replaceChildren()
      tray.hidden = true
    },
  }
}
