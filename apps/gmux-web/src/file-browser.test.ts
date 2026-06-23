import { describe, expect, it } from 'vitest'
import { closeFileBrowserPath, fileApiPath, fileBrowserPath, formatBytes, parentPath, pathSegments, projectFileBrowserPath } from './file-browser'

describe('file browser helpers', () => {
  it('builds overlay route and session API URLs', () => {
    expect(fileBrowserPath('sess-1', 'src/main.ts', '/gmux', '?settings=projects'))
      .toBe('/gmux?settings=projects&files=sess-1&filePath=src%2Fmain.ts')
    expect(projectFileBrowserPath('gd-idle', '', '/gd-idle', '?files=sess-1'))
      .toBe('/gd-idle?projectFiles=gd-idle')
    expect(closeFileBrowserPath('/gmux', '?settings=projects&files=sess-1&projectFiles=gd-idle&filePath=src'))
      .toBe('/gmux?settings=projects')
    expect(fileApiPath('list', { sessionId: 'sess 1' }, ''))
      .toBe('/v1/sessions/sess%201/files')
    expect(fileApiPath('content', { sessionId: 'sess/1' }, 'README.md'))
      .toBe('/v1/sessions/sess%2F1/file?path=README.md')
    expect(fileApiPath('raw', { sessionId: 'sess/1' }, 'assets/cat.png'))
      .toBe('/v1/sessions/sess%2F1/file?path=assets%2Fcat.png&raw=1')
    expect(fileApiPath('list', { projectSlug: 'gd-idle' }, ''))
      .toBe('/v1/projects/gd-idle/files')
  })

  it('handles breadcrumbs and parents', () => {
    expect(parentPath('src/app/main.ts')).toBe('src/app')
    expect(parentPath('src')).toBe('')
    expect(pathSegments('src/app/main.ts')).toEqual([
      { name: 'src', path: 'src' },
      { name: 'app', path: 'src/app' },
      { name: 'main.ts', path: 'src/app/main.ts' },
    ])
  })

  it('formats byte counts', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(12 * 1024)).toBe('12 KB')
  })
})
