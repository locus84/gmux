import { describe, expect, it, vi } from 'vitest'
import type { ILink, Terminal } from '@xterm/xterm'
import {
  createTerminalFileLinkProvider,
  findTerminalFileMatches,
  resolveTerminalFilePath,
  resolveTerminalPasteImageName,
  terminalFileTargetAtPoint,
  type TerminalFileLinkContext,
} from './terminal-file-link'

const rootContext: TerminalFileLinkContext = {
  sessionId: 'sess-1',
  root: '/work/repo',
  cwd: '/work/repo',
}

describe('resolveTerminalFilePath', () => {
  it('resolves relative paths from cwd into the session root', () => {
    expect(resolveTerminalFilePath('.pi/tmp/result.png', rootContext))
      .toBe('.pi/tmp/result.png')
    expect(resolveTerminalFilePath('./src/main.ts', rootContext))
      .toBe('src/main.ts')
    expect(resolveTerminalFilePath('../README.md', {
      ...rootContext,
      cwd: '/work/repo/packages/web',
    })).toBe('packages/README.md')
  })

  it('maps absolute paths inside the root and rejects paths outside it', () => {
    expect(resolveTerminalFilePath('/work/repo/assets/result.png', rootContext))
      .toBe('assets/result.png')
    expect(resolveTerminalFilePath('/work/other/secret.txt', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('/work/repo2/secret.txt', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('../secret.txt', rootContext)).toBeNull()
  })

  it('supports daemon-valid root and tilde roots without guessing home', () => {
    expect(resolveTerminalFilePath('/var/tmp/result.png', {
      ...rootContext,
      root: '/',
      cwd: '/var/tmp',
    })).toBe('var/tmp/result.png')
    expect(resolveTerminalFilePath('../result.png', {
      ...rootContext,
      root: '~',
      cwd: '~/project',
    })).toBe('result.png')
    expect(resolveTerminalFilePath('/Users/me/result.png', {
      ...rootContext,
      root: '~',
      cwd: '~',
    })).toBeNull()
  })

  it('recognizes only this session’s canonical gmux paste-image paths', () => {
    expect(resolveTerminalPasteImageName('/var/folders/T/gmux-pastes/sess-1/paste-37.jpg', rootContext)).toBe('paste-37.jpg')
    expect(resolveTerminalPasteImageName('/tmp/gmux-pastes/sess-1/paste-1.PNG', rootContext)).toBe('paste-1.PNG')
    expect(resolveTerminalPasteImageName('/tmp/gmux-pastes/sess-2/paste-1.png', rootContext)).toBeNull()
    expect(resolveTerminalPasteImageName('/tmp/paste-1.png', rootContext)).toBeNull()
    expect(resolveTerminalPasteImageName('/tmp/gmux-pastes/sess-1/paste-0.png', rootContext)).toBeNull()
    expect(resolveTerminalPasteImageName('/tmp/gmux-pastes/sess-1/paste-1.txt', rootContext)).toBeNull()
  })

  it('rejects URLs, fractions, and unsafe path characters', () => {
    expect(resolveTerminalFilePath('https://example.com/a.png', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('12/34', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('src/<script>.ts', rootContext)).toBeNull()
  })
})

describe('findTerminalFileMatches', () => {
  it('finds absolute paste images outside the workspace as temp targets', () => {
    expect(findTerminalFileMatches('/var/folders/T/gmux-pastes/sess-1/paste-37.jpg', rootContext)).toEqual([
      {
        text: '/var/folders/T/gmux-pastes/sess-1/paste-37.jpg',
        start: 0,
        path: 'paste-37.jpg',
        pasteImage: true,
      },
    ])
  })

  it('finds obvious paths and excludes surrounding punctuation and line references', () => {
    expect(findTerminalFileMatches(
      'see (`.pi/tmp/result.png`), then **src/main.ts:12:4**.',
      rootContext,
    )).toEqual([
      { text: '.pi/tmp/result.png', start: 6, path: '.pi/tmp/result.png' },
      { text: 'src/main.ts', start: 35, path: 'src/main.ts' },
    ])
  })

  it('does not turn web URLs or plain filenames into file links', () => {
    expect(findTerminalFileMatches(
      'visit https://example.com/a.png or read README.md',
      rootContext,
    )).toEqual([])
  })
})

describe('terminalFileTargetAtPoint', () => {
  it('resolves the visible buffer cell again after the viewport changes', () => {
    let viewportY = 10
    const rows = new Map<number, string>([
      [10, ' .pi/tmp/first.png'],
      [12, ' .pi/tmp/second.png'],
    ])
    const screen = { getBoundingClientRect: () => ({ left: 0, top: 0 }) }
    const term = {
      cols: 40,
      rows: 5,
      dimensions: { css: { cell: { width: 10, height: 20 } } },
      element: { querySelector: () => screen },
      buffer: {
        active: {
          get viewportY() { return viewportY },
          getLine: (y: number) => rows.has(y)
            ? { isWrapped: false, translateToString: () => rows.get(y)! }
            : undefined,
        },
      },
    } as unknown as Terminal

    expect(terminalFileTargetAtPoint(term, rootContext, 25, 10)).toMatchObject({ path: '.pi/tmp/first.png' })
    viewportY = 12
    expect(terminalFileTargetAtPoint(term, rootContext, 25, 10)).toMatchObject({ path: '.pi/tmp/second.png' })
  })

  it('does not resolve the active input row', () => {
    const screen = { getBoundingClientRect: () => ({ left: 0, top: 0 }) }
    const term = {
      cols: 40,
      rows: 5,
      dimensions: { css: { cell: { width: 10, height: 20 } } },
      element: { querySelector: () => screen },
      buffer: {
        active: {
          viewportY: 10,
          baseY: 10,
          cursorY: 0,
          getLine: (y: number) => y === 10
            ? { isWrapped: false, translateToString: () => ' .pi/tmp/input.png' }
            : undefined,
        },
      },
    } as unknown as Terminal

    expect(terminalFileTargetAtPoint(term, rootContext, 25, 10)).toBeNull()
  })
})

describe('createTerminalFileLinkProvider', () => {
  it('returns xterm ranges and opens with the captured session context', () => {
    const term = {
      buffer: {
        active: {
          getLine: (line: number) => line === 6
            ? { translateToString: () => 'file: .pi/tmp/result.png' }
            : undefined,
        },
      },
    } as unknown as Terminal
    const openFile = vi.fn()
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (sessionId, path) => `/viewer?files=${sessionId}&path=${path}`,
      openFile,
    )
    let links: ILink[] | undefined

    provider.provideLinks(7, value => { links = value })

    expect(links).toHaveLength(1)
    expect(links?.[0]).toMatchObject({
      text: '/viewer?files=sess-1&path=.pi/tmp/result.png',
      range: {
        start: { x: 7, y: 7 },
        end: { x: 24, y: 7 },
      },
    })
    links?.[0].activate({} as MouseEvent, links[0].text)
    expect(openFile).toHaveBeenCalledWith('sess-1', '.pi/tmp/result.png', false)
  })

  it('links a path that wraps across terminal buffer rows', () => {
    const lines = [
      { isWrapped: false, translateToString: () => 'path: .pi/tmp/ui_' },
      { isWrapped: true, translateToString: () => 'compare/result.png' },
    ]
    const term = {
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined

    provider.provideLinks(2, value => { links = value })
    expect(links?.[0]).toMatchObject({
      text: '/viewer?path=.pi/tmp/ui_compare/result.png',
      range: {
        start: { x: 7, y: 1 },
        end: { x: 18, y: 2 },
      },
    })
  })

  it('links a path manually wrapped by a full-screen TUI', () => {
    const lines = [
      { isWrapped: false, translateToString: () => ' .pi/tmp/ui-concepts/combat-modules-expand-desig' },
      { isWrapped: false, translateToString: () => ' n-board-v7.png                                  ' },
      { isWrapped: false, translateToString: () => ' next UI row                                     ' },
    ]
    const term = {
      cols: 49,
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )

    for (const bufferLineNumber of [1, 2]) {
      let links: ILink[] | undefined
      provider.provideLinks(bufferLineNumber, value => { links = value })
      expect(links?.[0]).toMatchObject({
        text: '/viewer?path=.pi/tmp/ui-concepts/combat-modules-expand-design-board-v7.png',
        range: {
          start: { x: 2, y: 1 },
          end: { x: 15, y: 2 },
        },
      })
    }
  })

  it('joins a manually wrapped paste path before trailing prose', () => {
    const lines = [
      { isWrapped: false, translateToString: () => ' /var/folders/cg/example12/T/gmux-pastes/sess-1/' },
      { isWrapped: false, translateToString: () => ' paste-1.jpg 이거 봐                              ' },
    ]
    const term = {
      cols: 49,
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path, pasteImage) => `/viewer?path=${path}&paste=${pasteImage}`,
      vi.fn(),
    )
    let links: ILink[] | undefined

    provider.provideLinks(1, value => { links = value })
    expect(links?.[0].text).toBe('/viewer?path=paste-1.jpg&paste=true')
  })

  it('does not merge unrelated full-width TUI rows into a path', () => {
    const lines = [
      { isWrapped: false, translateToString: () => ' .pi/tmp/not-a-complete-manually-wrapped-path-xx' },
      { isWrapped: false, translateToString: () => ' status                                  ' },
    ]
    const term = {
      cols: 49,
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined = []

    provider.provideLinks(1, value => { links = value })
    expect(links).toBeUndefined()
  })

  it('joins a file extension split across manually wrapped rows', () => {
    const lines = [
      { isWrapped: false, translateToString: () => ' .pi/tmp/combat-modules-atlas-audit-normal.p' },
      { isWrapped: false, translateToString: () => ' ng                                          ' },
    ]
    const term = {
      // Pi tool blocks can wrap inside a panel before the terminal edge.
      cols: 49,
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined

    provider.provideLinks(1, value => { links = value })
    expect(links?.[0].text).toBe('/viewer?path=.pi/tmp/combat-modules-atlas-audit-normal.png')
    expect(links?.[0].range.end.y).toBe(2)
  })

  it('does not append an unrelated row to an already complete edge-aligned path', () => {
    const lines = [
      { isWrapped: false, translateToString: () => ' prefix-padding-123456789 .pi/tmp/report.png' },
      { isWrapped: false, translateToString: () => ' status                                          ' },
    ]
    const term = {
      cols: 45,
      buffer: { active: { getLine: (y: number) => lines[y] } },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined

    provider.provideLinks(1, value => { links = value })
    expect(links?.[0].text).toBe('/viewer?path=.pi/tmp/report.png')
    expect(links?.[0].range.end.y).toBe(1)
  })

  it('does not linkify the active terminal input line', () => {
    const term = {
      buffer: {
        active: {
          baseY: 5,
          cursorY: 2,
          getLine: () => ({ isWrapped: false, translateToString: () => '.pi/tmp/input.png' }),
        },
      },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined = []

    provider.provideLinks(8, value => { links = value })
    expect(links).toBeUndefined()
  })

  it('maps UTF-16 text offsets to xterm cell columns for wide glyphs', () => {
    const cells = [
      { chars: '📁', width: 2 }, { chars: '', width: 0 },
      { chars: ' ', width: 1 },
      { chars: '결', width: 2 }, { chars: '', width: 0 },
      { chars: '과', width: 2 }, { chars: '', width: 0 },
      { chars: ' ', width: 1 },
      ...[...'.pi/tmp/a.png'].map(chars => ({ chars, width: 1 })),
    ]
    const term = {
      buffer: {
        active: {
          getLine: () => ({
            length: cells.length,
            getCell: (x: number) => cells[x]
              ? { getChars: () => cells[x].chars, getWidth: () => cells[x].width }
              : undefined,
            translateToString: () => '📁 결과 .pi/tmp/a.png',
          }),
        },
      },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined

    provider.provideLinks(1, value => { links = value })
    expect(links?.[0].range).toEqual({
      start: { x: 9, y: 1 },
      end: { x: 21, y: 1 },
    })
  })

  it('reads the current session context each time links are provided', () => {
    let context = rootContext
    const term = {
      buffer: { active: { getLine: () => ({ translateToString: () => 'src/main.ts' }) } },
    } as unknown as Terminal
    const openFile = vi.fn()
    const provider = createTerminalFileLinkProvider(
      term,
      () => context,
      (_sessionId, path) => `/viewer?path=${path}`,
      openFile,
    )
    let links: ILink[] | undefined

    provider.provideLinks(1, value => { links = value })
    context = { sessionId: 'sess-2', root: '/other/repo', cwd: '/other/repo' }
    provider.provideLinks(1, value => { links = value })
    links?.[0].activate({} as MouseEvent, links[0].text)

    expect(openFile).toHaveBeenCalledWith('sess-2', 'src/main.ts', false)
  })

  it('returns undefined when the buffer line has no safe path', () => {
    const term = {
      buffer: {
        active: {
          getLine: () => ({ translateToString: () => 'no files here' }),
        },
      },
    } as unknown as Terminal
    const provider = createTerminalFileLinkProvider(
      term,
      () => rootContext,
      (_sessionId, path) => `/viewer?path=${path}`,
      vi.fn(),
    )
    let links: ILink[] | undefined = []

    provider.provideLinks(1, value => { links = value })
    expect(links).toBeUndefined()
  })
})
