import { useEffect, useMemo, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import {
  closeFileBrowserPath,
  fileApiPath,
  fileBrowserPath,
  projectFileBrowserPath,
  formatBytes,
  parentPath,
  pathSegments,
  tempFileApiPath,
  type FileContentData,
  type FileEntry,
  type FileListData,
} from './file-browser'

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

type FileTarget = { kind: 'session'; id: string } | { kind: 'project'; slug: string } | { kind: 'paste'; id: string }
type ImageMode = 'fit' | 'actual' | 'fill'
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
  if (target.kind === 'project') return `project ${target.slug}`
  return target.kind === 'paste' ? 'temporary image' : `session ${target.id}`
}

function targetApi(target: Exclude<FileTarget, { kind: 'paste' }>): { sessionId: string } | { projectSlug: string } {
  return target.kind === 'session' ? { sessionId: target.id } : { projectSlug: target.slug }
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

  const apiTarget = targetApi(target)
  const listResp = await fetch(fileApiPath('list', apiTarget, path))
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

  const contentResp = await fetch(fileApiPath('content', apiTarget, path))
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
  const projectSlug = `${loc.query.projectFiles ?? ''}`
  const pasteSessionId = `${loc.query.pasteFile ?? ''}`
  const target: FileTarget | null = pasteSessionId
    ? { kind: 'paste', id: pasteSessionId }
    : sessionId
      ? { kind: 'session', id: sessionId }
      : projectSlug ? { kind: 'project', slug: projectSlug } : null
  const path = `${loc.query.filePath ?? ''}`
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [copied, setCopied] = useState<string | null>(null)
  const [imageMode, setImageModeState] = useState<ImageMode>(readImageMode)
  const [imageLoad, setImageLoad] = useState<ImageLoad>({ src: '', state: 'loading' })

  const segments = useMemo(() => pathSegments(path), [path])
  const title = path ? path.split('/').filter(Boolean).at(-1) || 'Files' : 'Files'
  // Paste targets are known images by construction. Start the raw request on
  // the first render instead of waiting for the metadata round trip.
  const imageSrc = target?.kind === 'paste'
    ? tempFileApiPath('raw', target.id, path)
    : target && state.kind === 'content' && state.data.kind === 'image'
      ? fileApiPath('raw', targetApi(target), path)
      : ''
  const imageLoadState = imageLoad.src === imageSrc ? imageLoad.state : 'loading'
  const copyPath = state.kind === 'list' || state.kind === 'content' ? state.data.abs_path : ''
  const close = () => loc.route(closeFileBrowserPath(loc.path, loc.url.includes('?') ? loc.url.slice(loc.url.indexOf('?')) : ''), true)

  useEffect(() => {
    let cancelled = false
    if (!target) {
      setState({ kind: 'error', code: 'bad_request', message: 'Missing file browser target' })
      return
    }
    setState({ kind: 'loading' })
    loadFileBrowser(target, path)
      .then(next => { if (!cancelled) setState(next) })
      .catch(err => { if (!cancelled) setState({ kind: 'error', message: err instanceof Error ? err.message : 'Failed to load files' }) })
    return () => { cancelled = true }
  }, [target?.kind, target?.kind === 'project' ? target.slug : target?.id, path])

  const routeTo = (nextPath: string) => {
    if (!target) return
    const search = loc.url.includes('?') ? loc.url.slice(loc.url.indexOf('?')) : ''
    if (target.kind === 'paste') return
    loc.route(target.kind === 'session'
      ? fileBrowserPath(target.id, nextPath, loc.path, search)
      : projectFileBrowserPath(target.slug, nextPath, loc.path, search))
  }
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      close()
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [loc.path, loc.url])

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
            <div class="file-image-mode" role="group" aria-label="Image sizing">
              <button class={imageMode === 'fit' ? 'active' : ''} onClick={() => setImageMode('fit')}>Fit</button>
              <button class={imageMode === 'actual' ? 'active' : ''} onClick={() => setImageMode('actual')}>100%</button>
              <button class={imageMode === 'fill' ? 'active' : ''} onClick={() => setImageMode('fill')}>Fill</button>
            </div>
          </div>
          <div class={`file-image-preview-wrap is-${imageMode}`}>
            {imageLoadState === 'loading' && <div class="state-subtitle">Loading image…</div>}
            {imageLoadState === 'error' && <div class="file-error"><div class="state-title">Could not load image</div></div>}
            <img
              key={imageSrc}
              class={`file-image-preview is-${imageMode}`}
              src={imageSrc}
              alt={state.kind === 'content' ? state.data.name : path}
              draggable={false}
              onLoad={() => setImageLoad({ src: imageSrc, state: 'loaded' })}
              onError={() => setImageLoad({ src: imageSrc, state: 'error' })}
            />
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
