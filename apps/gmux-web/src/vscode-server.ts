export function expandVSCodeWorkspacePath(workspacePath: string, homeDir?: string): string {
  const path = workspacePath.trim()
  const home = homeDir?.trim().replace(/\/$/, '')
  if (!home) return path
  if (path === '~') return home
  if (path.startsWith('~/')) return `${home}/${path.slice(2)}`
  return path
}

function encodeFolderParam(path: string): string {
  return encodeURIComponent(path)
    .replace(/%2F/g, '/')
    .replace(/%7E/g, '~')
}

export function buildVSCodeServerUrl(
  baseUrl: string | null | undefined,
  workspacePath: string | null | undefined,
  homeDir?: string | null,
): string | null {
  const base = baseUrl?.trim()
  const rawFolder = workspacePath?.trim()
  if (!base || !rawFolder) return null

  try {
    const url = new URL(base)
    const folder = expandVSCodeWorkspacePath(rawFolder, homeDir ?? undefined)
    const pairs = Array.from(url.searchParams.entries()).filter(([key]) => key !== 'folder')
    const query = pairs.map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`)
    query.push(`folder=${encodeFolderParam(folder)}`)
    url.search = query.join('&')
    return url.toString()
  } catch {
    return null
  }
}
