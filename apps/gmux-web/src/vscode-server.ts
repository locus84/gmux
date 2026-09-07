export interface VSCodeServerConfigValidation {
  values: { vsCodeServerUrl: string; vsCodeServerHomeDir: string }
  errors: { vsCodeServerUrl?: string; vsCodeServerHomeDir?: string }
}

export function validateVSCodeServerConfig(serverUrl: string, homeDir: string): VSCodeServerConfigValidation {
  const vsCodeServerUrl = serverUrl.trim()
  const vsCodeServerHomeDir = homeDir.trim()
  const errors: VSCodeServerConfigValidation['errors'] = {}

  if (vsCodeServerUrl.length > 2048) {
    errors.vsCodeServerUrl = 'The URL must be 2048 characters or fewer.'
  } else if (vsCodeServerUrl) {
    try {
      const parsed = new URL(vsCodeServerUrl)
      if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || !parsed.host || parsed.username || parsed.password) {
        errors.vsCodeServerUrl = 'Enter an absolute HTTP(S) URL without credentials.'
      }
    } catch {
      errors.vsCodeServerUrl = 'Enter an absolute HTTP(S) URL without credentials.'
    }
  }
  if (vsCodeServerHomeDir.length > 4096) {
    errors.vsCodeServerHomeDir = 'The path must be 4096 characters or fewer.'
  } else if (vsCodeServerHomeDir && !vsCodeServerHomeDir.startsWith('/')) {
    errors.vsCodeServerHomeDir = 'Enter an absolute server path beginning with /.'
  }
  if (/\p{Cc}|\u2028|\u2029/u.test(vsCodeServerUrl)) {
    errors.vsCodeServerUrl = 'The URL contains unsupported control characters.'
  }
  if (/\p{Cc}|\u2028|\u2029/u.test(vsCodeServerHomeDir)) {
    errors.vsCodeServerHomeDir = 'The path contains unsupported control characters.'
  }

  return {
    values: { vsCodeServerUrl, vsCodeServerHomeDir },
    errors,
  }
}

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

function isLoopbackHostname(hostname: string): boolean {
  const host = hostname.toLowerCase()
  return host === 'localhost'
    || host.endsWith('.localhost')
    || host === '0.0.0.0'
    || host === '[::1]'
    || /^127(?:\.\d{1,3}){3}$/.test(host)
}

/**
 * Convert a loopback (or common wildcard-bind) HTTP URL emitted by a local
 * terminal into code-server's generic same-host port proxy. Returns null when
 * no safe rewrite applies.
 */
export function buildVSCodeLoopbackProxyUrl(
  baseUrl: string | null | undefined,
  rawUrl: string,
): string | null {
  const baseText = baseUrl?.trim()
  if (!baseText) return null

  try {
    const source = new URL(rawUrl)
    const base = new URL(baseText)
    if (source.protocol !== 'http:'
      || !isLoopbackHostname(source.hostname)
      || source.username
      || source.password
      || (base.protocol !== 'https:' && base.protocol !== 'http:')) return null

    const port = source.port || '80'
    const basePath = base.pathname.endsWith('/') ? base.pathname : `${base.pathname}/`
    base.pathname = `${basePath}proxy/${port}${source.pathname}`
    base.search = source.search
    base.hash = source.hash
    return base.toString()
  } catch {
    return null
  }
}

/** Resolve a terminal URL without sending a peer's loopback to this host. */
export function resolveTerminalWebUrl(
  rawUrl: string,
  baseUrl: string | null | undefined,
  peer?: string,
): string {
  if (peer) return rawUrl
  return buildVSCodeLoopbackProxyUrl(baseUrl, rawUrl) ?? rawUrl
}
