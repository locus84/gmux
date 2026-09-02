import { describe, expect, it } from 'vitest'
import { clampImageZoom, closeFileBrowserPath, coverImageSize, fileApiPath, fileBrowserPath, formatBytes, imageSizeForMode, parentPath, pasteFileBrowserPath, pathSegments, tempFileApiPath, wheelImageZoom } from './file-browser'

describe('file browser helpers', () => {
  it('builds overlay route and session API URLs', () => {
    expect(fileBrowserPath('sess-1', 'src/main.ts', '/gmux', '?settings=projects'))
      .toBe('/gmux?settings=projects&files=sess-1&filePath=src%2Fmain.ts')
    expect(pasteFileBrowserPath('sess/peer', 'paste-37.jpg', '/gd-idle', '?files=sess-1'))
      .toBe('/gd-idle?pasteFile=sess%2Fpeer&filePath=paste-37.jpg')
    expect(closeFileBrowserPath('/gmux', '?settings=projects&files=sess-1&projectFiles=gd-idle&pasteFile=sess-2&filePath=src'))
      .toBe('/gmux?settings=projects')
    expect(fileApiPath('list', 'sess 1', ''))
      .toBe('/v1/sessions/sess%201/files')
    expect(fileApiPath('content', 'sess/1', 'README.md'))
      .toBe('/v1/sessions/sess%2F1/file?path=README.md')
    expect(fileApiPath('raw', 'sess/1', 'assets/cat.png'))
      .toBe('/v1/sessions/sess%2F1/file?path=assets%2Fcat.png&raw=1')
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

  it('sizes each image mode without changing aspect ratio', () => {
    expect(imageSizeForMode('fit', 800, 400, 360, 600)).toEqual({ width: 360, height: 180 })
    expect(imageSizeForMode('actual', 800, 400, 360, 600)).toEqual({ width: 800, height: 400 })
    expect(coverImageSize(800, 400, 360, 600)).toEqual({ width: 1200, height: 600 })
    expect(coverImageSize(400, 800, 360, 600)).toEqual({ width: 360, height: 720 })
    expect(coverImageSize(600, 1000, 360, 600)).toEqual({ width: 360, height: 600 })
    expect(imageSizeForMode('fit', 0, 400, 360, 600)).toBeNull()
  })

  it('clamps image zoom and changes wheel zoom by exactly five percent', () => {
    expect(wheelImageZoom(1, -1)).toBe(1.05)
    expect(wheelImageZoom(1.05, 500)).toBe(1)
    expect(wheelImageZoom(1.033, -1) - 1.033).toBeCloseTo(0.05, 12)
    expect(wheelImageZoom(1, 0)).toBe(1)
    expect(clampImageZoom(0.01)).toBe(0.25)
    expect(clampImageZoom(9)).toBe(5)
  })

  it('formats byte counts', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    expect(formatBytes(12 * 1024)).toBe('12 KB')
  })
})
