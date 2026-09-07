import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createProjectWorktree,
  ensureProjectWorktrees,
  projectWorktreeInventories,
  removeProjectWorktree,
} from './store'

const inventory = {
  ok: true,
  data: {
    project_slug: 'gmux',
    primary_path: '/repo',
    worktrees: [{ path: '/repo', branch: 'main', primary: true }],
  },
}

beforeEach(() => {
  projectWorktreeInventories.value = {}
  vi.restoreAllMocks()
})

describe('project worktree requests', () => {
  it('loads peer inventories through the owning daemon proxy', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(inventory), { status: 200 }))
    await ensureProjectWorktrees('gmux', 'dev box')
    expect(fetchMock).toHaveBeenCalledWith('/v1/peers/dev%20box/v1/projects/gmux/worktrees')
    expect(projectWorktreeInventories.value['dev box::gmux'].data?.primary_path).toBe('/repo')
  })

  it('creates and refreshes a linked checkout', async () => {
    const created = { ok: true, data: { project_slug: 'gmux', worktree: { path: '/work/fix', branch: 'fix', primary: false } } }
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(created), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory), { status: 200 }))
    const result = await createProjectWorktree('gmux', 'fix', 'main')
    expect(result.path).toBe('/work/fix')
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'POST' })
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({ branch: 'fix', base: 'main' })
  })

  it('removes and refreshes a linked checkout', async () => {
    const removed = { ok: true, data: { project_slug: 'gmux', removed_path: '/work/fix' } }
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(removed), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory), { status: 200 }))
    await removeProjectWorktree('gmux', '/work/fix')
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'DELETE' })
  })
})
