import { describe, expect, it } from 'vitest'
import { closeFileBrowserPath, coverImageSize, fileApiPath, fileBrowserPath, formatBytes, parentPath, pasteFileBrowserPath, pathSegments, projectFileBrowserPath, tempFileApiPath } from './file-browser'

describe('file browser helpers', () => {
  it('builds overlay route and session API URLs', () => {
    expect(fileBrowserPath('sess-1', 'src/main.ts', '/gmux', '?settings=projects'))
      .toBe('/gmux?settings=projects&files=sess-1&filePath=src%2Fmain.ts')
    expect(projectFileBrowserPath('gd-idle', '', '/gd-idle', '?files=sess-1'))
      .toBe('/gd-idle?projectFiles=gd-idle')
    expect(pasteFileBrowserPath('sess/peer', 'paste-37.jpg', '/gd-idle', '?files=sess-1'))
      .toBe('/gd-idle?pasteFile=sess%2Fpeer&filePath=paste-37.jpg')
    expect(closeFileBrowserPath('/gmux', '?settings=projects&files=sess-1&projectFiles=gd-idle&pasteFile=sess-2&filePath=src'))
      .toBe('/gmux?settings=projects')
    expect(fileApiPath('list', { sessionId: 'sess 1' }, ''))
      .toBe('/v1/sessions/sess%201/files')
    expect(fileApiPath('content', { sessionId: 'sess/1' }, 'README.md'))
      .toBe('/v1/sessions/sess%2F1/file?path=README.md')
    expect(fileApiPath('raw', { sessionId: 'sess/1' }, 'assets/cat.png'))
      .toBe('/v1/sessions/sess%2F1/file?path=assets%2Fcat.png&raw=1')
    expect(fileApiPath('list', { projectSlug: 'gd-idle' }, ''))
      .toBe('/v1/projects/gd-idle/files')
    expect(tempFileApiPath('raw', 'sess/peer', 'paste-37.jpg'))
      .toBe('/v1/sessions/sess%2Fpeer/temp-file?name=paste-37.jpg&raw=1')
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

  it('sizes images to cover the viewport without changing aspect ratio', () => {
    expect(coverImageSize(800, 400, 360, 600)).toEqual({ width: 1200, height: 600 })
    expect(coverImageSize(400, 800, 360, 600)).toEqual({ width: 360, height: 720 })
    expect(coverImageSize(600, 1000, 360, 600)).toEqual({ width: 360, height: 600 })
    expect(coverImageSize(0, 400, 360, 600)).toBeNull()
  })

  it('formats byte counts', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(12 * 1024)).toBe('12 KB')
  })
})
