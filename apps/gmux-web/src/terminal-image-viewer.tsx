import { createPortal } from 'preact/compat'
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks'
import { terminalImageUrl, type TerminalImageAsset } from './terminal-images'

const MAX_IMAGE_BYTES = 8 * 1024 * 1024
const MAX_IMAGE_DIMENSION = 8192
const MAX_IMAGE_PIXELS = 16_000_000

export type TerminalImageLoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; url: string; width: number; height: number }
  | { kind: 'error'; message: string; code?: string }

function pngDimensions(bytes: Uint8Array): { width: number; height: number } | null {
  const signature = [137, 80, 78, 71, 13, 10, 26, 10]
  if (bytes.length < 24 || !signature.every((value, index) => bytes[index] === value)) return null
  if (String.fromCharCode(...bytes.slice(12, 16)) !== 'IHDR') return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const width = view.getUint32(16)
  const height = view.getUint32(20)
  if (!width || !height || width > MAX_IMAGE_DIMENSION || height > MAX_IMAGE_DIMENSION || width * height > MAX_IMAGE_PIXELS) return null
  return { width, height }
}

async function responseError(response: Response): Promise<{ message: string; code?: string }> {
  const body = await response.json().catch(() => null) as { error?: { code?: string; message?: string } } | null
  const code = body?.error?.code
  if (code === 'image_expired' || code === 'image_not_found' || code === 'not_found') {
    return { code, message: 'This terminal image expired from the session cache. Retry after Pi redraws it.' }
  }
  return { code, message: body?.error?.message || `Could not load image (${response.status})` }
}

export async function fetchTerminalImage(
  sessionId: string,
  asset: TerminalImageAsset,
  signal: AbortSignal,
  fetcher: typeof fetch = fetch,
): Promise<{ blob: Blob; width: number; height: number }> {
  const response = await fetcher(terminalImageUrl(sessionId, asset.hash), {
    credentials: 'same-origin',
    signal,
  })
  if (!response.ok) throw await responseError(response)
  const contentType = response.headers.get('content-type')?.split(';', 1)[0].trim().toLowerCase()
  if (contentType !== 'image/png') {
    throw { code: 'invalid_image', message: 'Image response had an unexpected media type.' }
  }
  const contentLengthHeader = response.headers.get('content-length')
  const contentLength = contentLengthHeader === null ? null : Number(contentLengthHeader)
  if (contentLength !== null && Number.isFinite(contentLength) && (contentLength > MAX_IMAGE_BYTES || contentLength !== asset.bytes)) {
    throw { code: 'invalid_image', message: 'Image response size did not match its terminal reference.' }
  }

  let bytes: Uint8Array
  if (response.body) {
    const reader = response.body.getReader()
    const chunks: Uint8Array[] = []
    let length = 0
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      length += value.byteLength
      if (length > MAX_IMAGE_BYTES || length > asset.bytes) {
        await reader.cancel()
        throw { code: 'image_too_large', message: 'Image exceeded the safe preview size.' }
      }
      chunks.push(value)
    }
    bytes = new Uint8Array(length)
    let offset = 0
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength }
  } else {
    const buffer = await response.arrayBuffer()
    bytes = new Uint8Array(buffer)
  }
  if (bytes.byteLength !== asset.bytes || bytes.byteLength > MAX_IMAGE_BYTES) {
    throw { code: 'invalid_image', message: 'Image response size did not match its terminal reference.' }
  }
  const dimensions = pngDimensions(bytes)
  if (!dimensions) throw { code: 'invalid_image', message: 'Image is not a safe PNG preview.' }
  return { blob: new Blob([Uint8Array.from(bytes).buffer], { type: 'image/png' }), ...dimensions }
}

export function TerminalImageViewer({
  sessionId,
  asset,
  onClose,
}: {
  sessionId: string
  asset: TerminalImageAsset
  onClose: () => void
}) {
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<TerminalImageLoadState>({ kind: 'loading' })
  const dialogRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const controller = new AbortController()
    let objectUrl = ''
    setState({ kind: 'loading' })
    fetchTerminalImage(sessionId, asset, controller.signal)
      .then(result => {
        if (controller.signal.aborted) return
        objectUrl = URL.createObjectURL(result.blob)
        setState({ kind: 'ready', url: objectUrl, width: result.width, height: result.height })
      })
      .catch(error => {
        if (controller.signal.aborted) return
        const typed = error as { message?: string; code?: string }
        setState({ kind: 'error', code: typed.code, message: typed.message || 'Could not load terminal image.' })
      })
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [sessionId, asset.hash, asset.bytes, attempt])

  useLayoutEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    closeRef.current?.focus({ preventScroll: true })
    const onKeyDown = (event: KeyboardEvent) => {
      const dialog = dialogRef.current
      if (!dialog) return
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopImmediatePropagation()
        onClose()
        return
      }
      if (event.key === 'Tab') {
        const controls = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), [href], [tabindex]:not([tabindex="-1"])')]
        if (controls.length === 0) {
          event.preventDefault()
          closeRef.current?.focus({ preventScroll: true })
          return
        }
        const current = controls.indexOf(document.activeElement as HTMLElement)
        const next = event.shiftKey
          ? (current <= 0 ? controls.length - 1 : current - 1)
          : (current < 0 || current === controls.length - 1 ? 0 : current + 1)
        event.preventDefault()
        controls[next].focus({ preventScroll: true })
        return
      }
      // If focus was moved outside the modal by browser or extension code,
      // consume the key before xterm's textarea can receive terminal input.
      if (!dialog.contains(event.target as Node)) {
        event.preventDefault()
        event.stopImmediatePropagation()
        closeRef.current?.focus({ preventScroll: true })
      }
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => {
      window.removeEventListener('keydown', onKeyDown, true)
      if (previouslyFocused?.isConnected) previouslyFocused.focus({ preventScroll: true })
    }
  }, [onClose])

  return createPortal(
    <div ref={dialogRef} class="terminal-image-viewer" role="dialog" aria-modal="true" aria-label="Terminal image preview" onPointerDown={event => event.stopPropagation()}>
      <div class="terminal-image-viewer-header">
        <div>
          <strong>Terminal image</strong>
          <span>PNG · {asset.bytes.toLocaleString()} bytes</span>
        </div>
        <button ref={closeRef} type="button" onClick={onClose} aria-label="Close image preview">×</button>
      </div>
      <div class="terminal-image-viewer-body">
        {state.kind === 'loading' && <div class="state-message"><div class="state-subtitle">Loading image…</div></div>}
        {state.kind === 'ready' && (
          <img
            src={state.url}
            width={state.width}
            height={state.height}
            alt="Terminal output"
            onError={() => {
              URL.revokeObjectURL(state.url)
              setState({ kind: 'error', code: 'image_decode_failed', message: 'The PNG could not be decoded by this browser.' })
            }}
          />
        )}
        {state.kind === 'error' && (
          <div class="file-error">
            <div class="state-icon">⚠</div>
            <div class="state-title">Could not load image</div>
            <div class="state-subtitle">{state.message}</div>
            {state.code && <code>{state.code}</code>}
            <button type="button" class="btn" onClick={() => setAttempt(value => value + 1)}>Retry</button>
          </div>
        )}
      </div>
    </div>,
    document.body,
  )
}
