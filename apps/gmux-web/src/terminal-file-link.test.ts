import { describe, expect, it, vi } from 'vitest'
import type { ILink, Terminal } from '@xterm/xterm'
import {
  createTerminalFileLinkProvider,
  findTerminalFileMatches,
  resolveTerminalFilePath,
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

  it('rejects URLs, fractions, and unsafe path characters', () => {
    expect(resolveTerminalFilePath('https://example.com/a.png', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('12/34', rootContext)).toBeNull()
    expect(resolveTerminalFilePath('src/<script>.ts', rootContext)).toBeNull()
  })
})

describe('findTerminalFileMatches', () => {
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
    expect(openFile).toHaveBeenCalledWith('sess-1', '.pi/tmp/result.png')
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

    expect(openFile).toHaveBeenCalledWith('sess-2', 'src/main.ts')
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
