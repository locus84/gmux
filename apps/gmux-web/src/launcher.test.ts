import { describe, expect, test } from 'vitest'
import { launchersForPeer, formatTarget, launcherMenuPosition } from './launcher'
import type { LauncherDef, PeerInfo } from './types'

const localLaunchers: LauncherDef[] = [
  { id: 'shell', label: 'Shell', command: ['bash'], available: true },
  { id: 'claude', label: 'Claude', command: ['claude'], available: true },
]
const localDefault = 'shell'

const peersWithLaunchers: PeerInfo[] = [
  {
    name: 'work-laptop', url: 'https://work-laptop', status: 'connected',
    session_count: 2, default_launcher: 'pi', launchers: [
      { id: 'shell', label: 'Shell', command: ['zsh'], available: true },
      { id: 'pi', label: 'pi', command: ['pi'], available: true },
    ],
  },
]

describe('launchersForPeer', () => {
  test('returns local config when peer is undefined', () => {
    const resolved = launchersForPeer(localLaunchers, localDefault, peersWithLaunchers, undefined)
    expect(resolved.default_launcher).toBe('shell')
    expect(resolved.launchers.map(l => l.id)).toEqual(['shell', 'claude'])
  })

  test('returns peer config when peer matches', () => {
    const resolved = launchersForPeer(localLaunchers, localDefault, peersWithLaunchers, 'work-laptop')
    expect(resolved.default_launcher).toBe('pi')
    expect(resolved.launchers.map(l => l.id)).toEqual(['shell', 'pi'])
  })

  test('falls back to local when peer is unknown', () => {
    const resolved = launchersForPeer(localLaunchers, localDefault, peersWithLaunchers, 'mystery-host')
    expect(resolved.default_launcher).toBe('shell')
    expect(resolved.launchers.map(l => l.id)).toEqual(['shell', 'claude'])
  })

  test('falls back to local when peers list is empty', () => {
    const resolved = launchersForPeer(localLaunchers, localDefault, [], 'work-laptop')
    expect(resolved.default_launcher).toBe('shell')
  })
})

describe('launcherMenuPosition', () => {
  const viewport = { left: 0, top: 0, width: 390, height: 844 }

  test('right-aligns to the anchor and offsets the target line', () => {
    expect(launcherMenuPosition(
      { left: 300, right: 324, top: 120 },
      { width: 180, height: 120 },
      viewport,
      32,
    )).toEqual({ left: 144, top: 84, maxWidth: 374, maxHeight: 828 })
  })

  test('clamps all edges inside an offset visual viewport', () => {
    expect(launcherMenuPosition(
      { left: 400, right: 424, top: 560 },
      { width: 260, height: 400 },
      { left: 20, top: 200, width: 390, height: 300 },
      32,
    )).toEqual({ left: 142, top: 208, maxWidth: 374, maxHeight: 284 })
  })

  test('moves a bottom-edge menu above its anchor', () => {
    const pos = launcherMenuPosition(
      { left: 340, right: 364, top: 820 },
      { width: 180, height: 180 },
      viewport,
      0,
    )
    expect(pos.top).toBe(636)
    expect(pos.left).toBe(184)
  })

  test('constrains oversized menus to the viewport', () => {
    expect(launcherMenuPosition(
      { left: 4, right: 28, top: 4 },
      { width: 600, height: 1000 },
      viewport,
      0,
    )).toEqual({ left: 8, top: 8, maxWidth: 374, maxHeight: 828 })
  })
})

describe('formatTarget', () => {
  test('shows short cwd for local target', () => {
    expect(formatTarget({ cwd: '/home/mg/dev/gmux' })).toBe('~/dev/gmux')
  })

  test('prefixes peer name for remote target', () => {
    expect(formatTarget({ peer: 'laptop', cwd: '/workspace' })).toBe('laptop: /workspace')
  })

  test('shortens home dir even with peer', () => {
    expect(formatTarget({ peer: 'server', cwd: '/home/mg/work' })).toBe('server: ~/work')
  })
})
