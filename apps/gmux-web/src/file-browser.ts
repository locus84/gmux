export interface FileEntry {
  name: string
  path: string
  type: 'dir' | 'file' | 'symlink'
  size?: number
  mod_time?: string
  hidden?: boolean
  symlink?: boolean
  too_large?: boolean
}

export interface FileListData {
  root: string
  path: string
  abs_path: string
  entries: FileEntry[]
  truncated?: boolean
}

export interface FileContentData {
  root: string
  path: string
  abs_path: string
  name: string
  size: number
  mod_time?: string
  mime?: string
  kind?: 'text' | 'image'
  content: string
  truncated?: boolean
}

export function fileBrowserPath(sessionId: string, path = '', currentPath = location.pathname, currentSearch = location.search): string {
  return fileBrowserTargetPath('files', sessionId, path, currentPath, currentSearch)
}

export function projectFileBrowserPath(projectSlug: string, path = '', currentPath = location.pathname, currentSearch = location.search): string {
  return fileBrowserTargetPath('projectFiles', projectSlug, path, currentPath, currentSearch)
}

export function pasteFileBrowserPath(sessionId: string, name: string, currentPath = location.pathname, currentSearch = location.search): string {
  return fileBrowserTargetPath('pasteFile', sessionId, name, currentPath, currentSearch)
}

function fileBrowserTargetPath(key: 'files' | 'projectFiles' | 'pasteFile', value: string, path: string, currentPath: string, currentSearch: string): string {
  const params = new URLSearchParams(currentSearch)
  params.delete('files')
  params.delete('projectFiles')
  params.delete('pasteFile')
  params.set(key, value)
  if (path) params.set('filePath', path)
  else params.delete('filePath')
  return `${currentPath}?${params.toString()}`
}

export function closeFileBrowserPath(currentPath = location.pathname, currentSearch = location.search): string {
  const params = new URLSearchParams(currentSearch)
  params.delete('files')
  params.delete('projectFiles')
  params.delete('pasteFile')
  params.delete('filePath')
  const qs = params.toString()
  return qs ? `${currentPath}?${qs}` : currentPath
}

export function fileApiPath(kind: 'list' | 'content' | 'raw', target: { sessionId: string } | { projectSlug: string }, path = ''): string {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  if (kind === 'raw') params.set('raw', '1')
  const endpoint = kind === 'list' ? 'files' : 'file'
  const qs = params.toString()
  const base = 'sessionId' in target
    ? `/v1/sessions/${encodeURIComponent(target.sessionId)}/${endpoint}`
    : `/v1/projects/${encodeURIComponent(target.projectSlug)}/${endpoint}`
  return `${base}${qs ? `?${qs}` : ''}`
}

export function tempFileApiPath(kind: 'content' | 'raw', sessionId: string, name: string): string {
  const params = new URLSearchParams({ name })
  if (kind === 'raw') params.set('raw', '1')
  return `/v1/sessions/${encodeURIComponent(sessionId)}/temp-file?${params.toString()}`
}

export function parentPath(path: string): string {
  const clean = path.replace(/^\/+|\/+$/g, '')
  if (!clean) return ''
  const idx = clean.lastIndexOf('/')
  return idx < 0 ? '' : clean.slice(0, idx)
}

export function pathSegments(path: string): Array<{ name: string; path: string }> {
  const parts = path.split('/').filter(Boolean)
  let acc = ''
  return parts.map(name => {
    acc = acc ? `${acc}/${name}` : name
    return { name, path: acc }
  })
}

export function coverImageSize(
  naturalWidth: number,
  naturalHeight: number,
  viewportWidth: number,
  viewportHeight: number,
): { width: number; height: number } | null {
  if (naturalWidth <= 0 || naturalHeight <= 0 || viewportWidth <= 0 || viewportHeight <= 0) return null
  const scale = Math.max(viewportWidth / naturalWidth, viewportHeight / naturalHeight)
  return { width: naturalWidth * scale, height: naturalHeight * scale }
}

export function formatBytes(bytes?: number): string {
  if (!bytes) return bytes === 0 ? '0 B' : ''
  const units = ['B', 'KB', 'MB', 'GB']
  let n = bytes
  let idx = 0
  while (n >= 1024 && idx < units.length - 1) {
    n /= 1024
    idx++
  }
  return `${idx === 0 ? n : n.toFixed(n >= 10 ? 0 : 1)} ${units[idx]}`
}
