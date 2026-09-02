import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import {
  clampImageZoom,
  closeFileBrowserPath,
  fileApiPath,
  fileBrowserPath,
  formatBytes,
  imageSizeForMode,
  parentPath,
  pathSegments,
  tempFileApiPath,
  type FileContentData,
  type FileEntry,
  type FileListData,
  type ImageSizingMode,
  wheelImageZoom,
} from './file-browser'
import { vsCodeServerHomeDir, vsCodeServerUrl } from './store'
import { buildVSCodeServerUrl } from './vscode-server'

interface ApiEnvelope<T> {
  ok: boolean
  data?: T
  error?: { code?: string; message?: string }
}

type LoadState =
  | { kind: 'loading' }
  | { kind: 'list'; data: FileListData }
  | { kind: 'content'; data: FileContentData }
  | { kind: 'error'; message: string; code?: string }

function displayPath(root: string, path: string): string {
  return path ? `${root}/${path}` : root
}

type FileTarget = { kind: 'session'; id: string } | { kind: 'paste'; id: string }
type ImageMode = ImageSizingMode
type ImageLoadState = 'loading' | 'loaded' | 'error'
type ImageLoad = { src: string; state: ImageLoadState }

const imageModeKey = 'gmux:file-image-mode'

function readImageMode(): ImageMode {
  try {
    const mode = localStorage.getItem(imageModeKey)
    return mode === 'actual' || mode === 'fill' ? mode : 'fit'
  } catch {
    return 'fit'
  }
}

function saveImageMode(mode: ImageMode): void {
  try {
    localStorage.setItem(imageModeKey, mode)
  } catch {
    // Ignore private-mode/storage failures; the in-memory state still works.
  }
}

function targetLabel(target: FileTarget): string {
  return target.kind === 'paste' ? 'temporary image' : `session ${target.id}`
}

async function loadFileBrowser(target: FileTarget, path: string): Promise<LoadState> {
  if (target.kind === 'paste') {
    const resp = await fetch(tempFileApiPath('content', target.id, path))
    const body = await resp.json().catch(() => null) as ApiEnvelope<FileContentData> | null
    if (resp.ok && body?.ok && body.data) return { kind: 'content', data: body.data }
    return {
      kind: 'error',
      code: body?.error?.code,
      message: body?.error?.message || `Could not preview temporary image (${resp.status})`,
    }
  }

  const listResp = await fetch(fileApiPath('list', target.id, path))
  if (listResp.ok) {
    const body = await listResp.json() as ApiEnvelope<FileListData>
    if (body.ok && body.data) return { kind: 'list', data: body.data }
  }

  if (listResp.status === 400) {
    const body = await listResp.json().catch(() => null) as ApiEnvelope<unknown> | null
    if (body?.error?.code !== 'not_directory') {
      return { kind: 'error', code: body?.error?.code, message: body?.error?.message || 'Could not list directory' }
    }
  } else if (!listResp.ok) {
    const body = await listResp.json().catch(() => null) as ApiEnvelope<unknown> | null
    return { kind: 'error', code: body?.error?.code, message: body?.error?.message || `Request failed (${listResp.status})` }
  }

  const contentResp = await fetch(fileApiPath('content', target.id, path))
  const body = await contentResp.json().catch(() => null) as ApiEnvelope<FileContentData> | null
  if (contentResp.ok && body?.ok && body.data) return { kind: 'content', data: body.data }
  return {
    kind: 'error',
    code: body?.error?.code,
    message: body?.error?.message || `Could not preview file (${contentResp.status})`,
  }
}

function FileIcon({ entry }: { entry: FileEntry }) {
  if (entry.type === 'dir') return <span class="file-entry-icon">📁</span>
  if (entry.too_large) return <span class="file-entry-icon">⬚</span>
  return <span class="file-entry-icon">📄</span>
}

export function FileBrowserView() {
  const loc = useLocation()
  const sessionId = `${loc.query.files ?? ''}`
  const pasteSessionId = `${loc.query.pasteFile ?? ''}`
  const target: FileTarget | null = pasteSessionId
    ? { kind: 'paste', id: pasteSessionId }
    : sessionId ? { kind: 'session', id: sessionId } : null
  const path = `${loc.query.filePath ?? ''}`
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [copied, setCopied] = useState<string | null>(null)
  const [imageMode, setImageModeState] = useState<ImageMode>(readImageMode)
  const [imageLoad, setImageLoad] = useState<ImageLoad>({ src: '', state: 'loading' })
  const imageWrapRef = useRef<HTMLDivElement>(null)
  const imageStageRef = useRef<HTMLDivElement>(null)
  const imageRef = useRef<HTMLImageElement>(null)
  const imageZoomRef = useRef(1)
  const imageZoomLabelRef = useRef<HTMLSpanElement>(null)

  const segments = useMemo(() => pathSegments(path), [path])
  const title = path ? path.split('/').filter(Boolean).at(-1) || 'Files' : 'Files'
  // Paste targets are known images by construction. Start the raw request on
  // the first render instead of waiting for the metadata round trip.
  const imageSrc = target?.kind === 'paste'
    ? tempFileApiPath('raw', target.id, path)
    : target && state.kind === 'content' && state.data.kind === 'image'
      ? fileApiPath('raw', target.id, path)
      : ''
  const imageLoadState = imageLoad.src === imageSrc ? imageLoad.state : 'loading'
  const copyPath = state.kind === 'list' || state.kind === 'content' ? state.data.abs_path : ''
  const workspaceRoot = state.kind === 'list' || state.kind === 'content' ? state.data.root : ''
  const codeUrl = target?.kind === 'session'
    ? buildVSCodeServerUrl(vsCodeServerUrl.value, workspaceRoot, vsCodeServerHomeDir.value)
    : null
  const close = () => loc.route(closeFileBrowserPath(loc.path, loc.url.includes('?') ? loc.url.slice(loc.url.indexOf('?')) : ''), true)

  useEffect(() => {
    let cancelled = false
    if (!target) return
    setState({ kind: 'loading' })
    loadFileBrowser(target, path)
      .then(next => { if (!cancelled) setState(next) })
      .catch(err => { if (!cancelled) setState({ kind: 'error', message: err instanceof Error ? err.message : 'Failed to load files' }) })
    return () => { cancelled = true }
  }, [target?.kind, target?.id, path])

  const routeTo = (nextPath: string) => {
    if (!target) return
    const search = loc.url.includes('?') ? loc.url.slice(loc.url.indexOf('?')) : ''
    if (target.kind === 'paste') return
    loc.route(fileBrowserPath(target.id, nextPath, loc.path, search))
  }
  useEffect(() => {
    if (!target) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      close()
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [loc.path, loc.url])

  useEffect(() => {
    const wrap = imageWrapRef.current
    const stage = imageStageRef.current
    const image = imageRef.current
    if (!wrap || !stage || !image || imageLoadState !== 'loaded') return

    imageZoomRef.current = 1
    let centerFrame = 0
    let pinch: { x: number; y: number; distance: number } | null = null
    let wheelDelta = 0

    const touchMetrics = (touches: TouchList) => {
      const first = touches[0]
      const second = touches[1]
      return {
        x: (first.clientX + second.clientX) / 2,
        y: (first.clientY + second.clientY) / 2,
        distance: Math.hypot(second.clientX - first.clientX, second.clientY - first.clientY),
      }
    }

    const layout = (requestedZoom: number, focal?: { fromX: number; fromY: number; toX: number; toY: number }, center = false) => {
      const stageStyle = getComputedStyle(stage)
      const horizontalPadding = parseFloat(stageStyle.paddingLeft) + parseFloat(stageStyle.paddingRight)
      const verticalPadding = parseFloat(stageStyle.paddingTop) + parseFloat(stageStyle.paddingBottom)
      const base = imageSizeForMode(
        imageMode,
        image.naturalWidth,
        image.naturalHeight,
        wrap.clientWidth - horizontalPadding,
        wrap.clientHeight - verticalPadding,
      )
      if (!base) return

      const oldRect = focal ? image.getBoundingClientRect() : null
      const anchorX = oldRect && oldRect.width ? (focal!.fromX - oldRect.left) / oldRect.width : 0.5
      const anchorY = oldRect && oldRect.height ? (focal!.fromY - oldRect.top) / oldRect.height : 0.5
      const zoom = clampImageZoom(requestedZoom)
      const width = base.width * zoom
      const height = base.height * zoom
      imageZoomRef.current = zoom
      image.style.width = `${width}px`
      image.style.height = `${height}px`
      stage.style.width = `${Math.max(wrap.clientWidth, width + horizontalPadding)}px`
      stage.style.height = `${Math.max(wrap.clientHeight, height + verticalPadding)}px`
      if (imageZoomLabelRef.current) imageZoomLabelRef.current.textContent = `${Math.round(zoom * 100)}%`

      if (focal) {
        const newRect = image.getBoundingClientRect()
        wrap.scrollLeft += newRect.left + anchorX * newRect.width - focal.toX
        wrap.scrollTop += newRect.top + anchorY * newRect.height - focal.toY
      } else if (center) {
        cancelAnimationFrame(centerFrame)
        centerFrame = requestAnimationFrame(() => {
          wrap.scrollLeft = (wrap.scrollWidth - wrap.clientWidth) / 2
          wrap.scrollTop = (wrap.scrollHeight - wrap.clientHeight) / 2
        })
      }
    }

    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      const delta = event.deltaMode === WheelEvent.DOM_DELTA_LINE
        ? event.deltaY * 16
        : event.deltaMode === WheelEvent.DOM_DELTA_PAGE ? event.deltaY * wrap.clientHeight : event.deltaY
      wheelDelta += delta
      if (Math.abs(wheelDelta) < 40) return
      const direction = wheelDelta
      wheelDelta = 0
      layout(wheelImageZoom(imageZoomRef.current, direction), {
        fromX: event.clientX,
        fromY: event.clientY,
        toX: event.clientX,
        toY: event.clientY,
      })
    }
    const onTouchStart = (event: TouchEvent) => {
      if (event.touches.length !== 2) return
      event.preventDefault()
      pinch = touchMetrics(event.touches)
    }
    const onTouchMove = (event: TouchEvent) => {
      if (event.touches.length !== 2 || !pinch) return
      event.preventDefault()
      const next = touchMetrics(event.touches)
      layout(imageZoomRef.current * next.distance / pinch.distance, {
        fromX: pinch.x,
        fromY: pinch.y,
        toX: next.x,
        toY: next.y,
      })
      pinch = next
    }
    const endPinch = (event: TouchEvent) => {
      if (event.touches.length < 2) pinch = null
    }

    layout(1, undefined, true)
    const observer = new ResizeObserver(() => {
      const rect = wrap.getBoundingClientRect()
      const x = rect.left + rect.width / 2
      const y = rect.top + rect.height / 2
      layout(imageZoomRef.current, { fromX: x, fromY: y, toX: x, toY: y })
    })
    observer.observe(wrap)
    wrap.addEventListener('wheel', onWheel, { passive: false })
    wrap.addEventListener('touchstart', onTouchStart, { passive: false })
    wrap.addEventListener('touchmove', onTouchMove, { passive: false })
    wrap.addEventListener('touchend', endPinch)
    wrap.addEventListener('touchcancel', endPinch)
    return () => {
      observer.disconnect()
      cancelAnimationFrame(centerFrame)
      wrap.removeEventListener('wheel', onWheel)
      wrap.removeEventListener('touchstart', onTouchStart)
      wrap.removeEventListener('touchmove', onTouchMove)
      wrap.removeEventListener('touchend', endPinch)
      wrap.removeEventListener('touchcancel', endPinch)
    }
  }, [imageMode, imageSrc, imageLoadState])

  const setImageMode = (mode: ImageMode) => {
    setImageModeState(mode)
    saveImageMode(mode)
  }
  const copy = async (label: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(label)
      window.setTimeout(() => setCopied(null), 1500)
    } catch (err) {
      console.warn('copy failed:', err)
    }
  }

  if (!target) return null

  return (
    <div class="file-view">
      <div class="file-view-header">
        <button class="file-back-btn" onClick={() => target?.kind === 'paste' || !path ? close() : routeTo(parentPath(path))} aria-label="Back">
          ←
        </button>
        <div class="file-view-title-block">
          <div class="file-view-title">{title || 'Files'}</div>
          <div class="file-view-subtitle">{state.kind === 'list' || state.kind === 'content' ? displayPath(state.data.root, path) : target ? targetLabel(target) : 'Files'}</div>
        </div>
        {codeUrl && <button class="file-code-btn" onClick={() => window.open(codeUrl, '_blank', 'noopener')} title="Open workspace in VS Code Server">Code</button>}
        <button class="file-copy-btn" disabled={!copyPath} onClick={() => copyPath && copy('path', copyPath)}>Copy path</button>
        <button class="file-close-btn" onClick={close} aria-label="Close files">×</button>
      </div>

      {target?.kind !== 'paste' && (
        <div class="file-breadcrumbs" aria-label="Breadcrumbs">
          <button onClick={() => routeTo('')}>{state.kind === 'list' || state.kind === 'content' ? state.data.root : 'root'}</button>
          {segments.map(seg => <button key={seg.path} onClick={() => routeTo(seg.path)}>/ {seg.name}</button>)}
        </div>
      )}

      {copied && <div class="file-toast">Copied {copied}</div>}

      {state.kind === 'loading' && !imageSrc && (
        <div class="state-message"><div class="state-subtitle">Loading files…</div></div>
      )}

      {state.kind === 'error' && (
        <div class="file-error">
          <div class="state-icon">⚠</div>
          <div class="state-title">Could not open file</div>
          <div class="state-subtitle">{state.message}</div>
          {state.code && <code>{state.code}</code>}
        </div>
      )}

      {state.kind === 'list' && (
        <div class="file-list">
          {path && (
            <button class="file-entry" onClick={() => routeTo(parentPath(path))}>
              <span class="file-entry-icon">↩</span>
              <span class="file-entry-main"><span class="file-entry-name">..</span></span>
            </button>
          )}
          {state.data.entries.map(entry => (
            <button class="file-entry" key={entry.path} onClick={() => routeTo(entry.path)}>
              <FileIcon entry={entry} />
              <span class="file-entry-main">
                <span class="file-entry-name">{entry.name}{entry.type === 'dir' ? '/' : ''}</span>
                <span class="file-entry-meta">
                  {entry.symlink ? 'symlink · ' : ''}{entry.type === 'dir' ? 'folder' : formatBytes(entry.size)}
                  {entry.too_large ? ' · too large' : ''}
                </span>
              </span>
            </button>
          ))}
          {state.data.entries.length === 0 && <div class="file-empty">Empty directory</div>}
          {state.data.truncated && <div class="file-empty">Showing first entries only</div>}
        </div>
      )}

      {imageSrc && state.kind !== 'error' && (
        <div class="file-content-panel">
          <div class="file-content-actions">
            <span>{state.kind === 'content'
              ? `${formatBytes(state.data.size)}${state.data.mime ? ` · ${state.data.mime}` : ''}`
              : 'Loading image…'}</span>
            <span ref={imageZoomLabelRef} class="file-image-zoom-level">100%</span>
            <div class="file-image-mode" role="group" aria-label="Image sizing">
              <button class={imageMode === 'fit' ? 'active' : ''} onClick={() => setImageMode('fit')}>Fit</button>
              <button class={imageMode === 'actual' ? 'active' : ''} onClick={() => setImageMode('actual')}>100%</button>
              <button class={imageMode === 'fill' ? 'active' : ''} onClick={() => setImageMode('fill')}>Fill</button>
            </div>
          </div>
          <div ref={imageWrapRef} class={`file-image-preview-wrap is-${imageMode}`}>
            {imageLoadState === 'loading' && <div class="state-subtitle">Loading image…</div>}
            {imageLoadState === 'error' && <div class="file-error"><div class="state-title">Could not load image</div></div>}
            <div ref={imageStageRef} class="file-image-stage">
              <img
                key={imageSrc}
                ref={imageRef}
                class={`file-image-preview is-${imageMode}`}
                src={imageSrc}
                alt={state.kind === 'content' ? state.data.name : path}
                draggable={false}
                onLoad={() => setImageLoad({ src: imageSrc, state: 'loaded' })}
                onError={() => setImageLoad({ src: imageSrc, state: 'error' })}
              />
            </div>
          </div>
        </div>
      )}

      {state.kind === 'content' && !imageSrc && (
        <div class="file-content-panel">
          <div class="file-content-actions">
            <span>{formatBytes(state.data.size)}{state.data.mime ? ` · ${state.data.mime}` : ''}</span>
            <button class="file-copy-btn" onClick={() => copy('content', state.data.content)}>Copy content</button>
          </div>
          <pre class="file-content"><code>{state.data.content}</code></pre>
        </div>
      )}
    </div>
  )
}
