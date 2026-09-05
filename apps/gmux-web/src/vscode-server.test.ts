import { describe, expect, it } from 'vitest'
import { buildVSCodeLoopbackProxyUrl, buildVSCodeServerUrl, expandVSCodeWorkspacePath, resolveTerminalWebUrl, validateVSCodeServerConfig } from './vscode-server'

describe('validateVSCodeServerConfig', () => {
  it('trims valid optional values', () => {
    expect(validateVSCodeServerConfig(' https://code.example.test/base/ ', ' /home/rhee ')).toEqual({
      values: { vsCodeServerUrl: 'https://code.example.test/base/', vsCodeServerHomeDir: '/home/rhee' },
      errors: {},
    })
    expect(validateVSCodeServerConfig('', '')).toEqual({
      values: { vsCodeServerUrl: '', vsCodeServerHomeDir: '' },
      errors: {},
    })
  })

  it('rejects unsafe URLs and non-absolute home directories', () => {
    for (const value of ['code.example.test', 'file:///tmp/code', 'https://user:pass@example.test']) {
      expect(validateVSCodeServerConfig(value, '').errors.vsCodeServerUrl).toBeTruthy()
    }
    expect(validateVSCodeServerConfig('https://code.example.test', '~/repo').errors.vsCodeServerHomeDir).toBeTruthy()
    expect(validateVSCodeServerConfig('https://code.example.test', 'home/rhee').errors.vsCodeServerHomeDir).toBeTruthy()
  })
})

describe('buildVSCodeServerUrl', () => {
  it('adds the workspace path as a folder query parameter', () => {
    expect(buildVSCodeServerUrl('https://code.example.test', '/Users/rhee/WorkSpace/gmux'))
      .toBe('https://code.example.test/?folder=/Users/rhee/WorkSpace/gmux')
  })

  it('preserves existing query parameters', () => {
    expect(buildVSCodeServerUrl('https://code.example.test/?window=1', '/repo'))
      .toBe('https://code.example.test/?window=1&folder=/repo')
  })

  it('replaces an existing folder parameter', () => {
    expect(buildVSCodeServerUrl('https://code.example.test/?folder=/old', '/repo'))
      .toBe('https://code.example.test/?folder=/repo')
  })

  it('expands tilde paths when a home directory is configured', () => {
    expect(expandVSCodeWorkspacePath('~/WorkSpace/gmux', '/Users/rhee/'))
      .toBe('/Users/rhee/WorkSpace/gmux')
    expect(buildVSCodeServerUrl('https://code.example.test', '~/WorkSpace/gmux', '/Users/rhee'))
      .toBe('https://code.example.test/?folder=/Users/rhee/WorkSpace/gmux')
  })

  it('returns null when unset or invalid', () => {
    expect(buildVSCodeServerUrl('', '/repo')).toBeNull()
    expect(buildVSCodeServerUrl('https://code.example.test', '')).toBeNull()
    expect(buildVSCodeServerUrl('not a url', '/repo')).toBeNull()
  })
})

describe('buildVSCodeLoopbackProxyUrl', () => {
  it('rewrites loopback hosts through the code-server port proxy', () => {
    const base = 'https://gmux-vscode.example.test/'
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://localhost:8766/'))
      .toBe('https://gmux-vscode.example.test/proxy/8766/')
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://127.0.0.2:3000/app'))
      .toBe('https://gmux-vscode.example.test/proxy/3000/app')
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://[::1]:8080/'))
      .toBe('https://gmux-vscode.example.test/proxy/8080/')
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://0.0.0.0:4173/'))
      .toBe('https://gmux-vscode.example.test/proxy/4173/')
  })

  it('preserves base paths and source path, query, and fragment', () => {
    expect(buildVSCodeLoopbackProxyUrl(
      'https://code.example.test/code/?folder=/old',
      'http://localhost:9000/game/index.html?debug=1#play',
    )).toBe('https://code.example.test/code/proxy/9000/game/index.html?debug=1#play')
  })

  it('uses port 80 when an HTTP loopback URL omits its port', () => {
    expect(buildVSCodeLoopbackProxyUrl(
      'https://code.example.test/',
      'http://localhost/status',
    )).toBe('https://code.example.test/proxy/80/status')
  })

  it('does not rewrite remote, HTTPS, credentialed, or invalid URLs', () => {
    const base = 'https://code.example.test/'
    expect(buildVSCodeLoopbackProxyUrl(base, 'https://localhost:8443/')).toBeNull()
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://example.com:8766/')).toBeNull()
    expect(buildVSCodeLoopbackProxyUrl(base, 'http://user:pass@localhost:8766/')).toBeNull()
    expect(buildVSCodeLoopbackProxyUrl(base, 'not a url')).toBeNull()
    expect(buildVSCodeLoopbackProxyUrl('', 'http://localhost:8766/')).toBeNull()
  })

  it('resolves local links but leaves peer loopback on its owning host', () => {
    const raw = 'http://localhost:8766/game'
    const base = 'https://code.example.test/'
    expect(resolveTerminalWebUrl(raw, base))
      .toBe('https://code.example.test/proxy/8766/game')
    expect(resolveTerminalWebUrl(raw, base, 'remote-mac')).toBe(raw)
    expect(resolveTerminalWebUrl('https://example.com/', base)).toBe('https://example.com/')
  })
})
