import { describe, expect, it } from 'vitest'
import { buildVSCodeServerUrl, expandVSCodeWorkspacePath } from './vscode-server'

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
