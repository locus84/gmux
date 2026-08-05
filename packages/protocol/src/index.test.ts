import { describe, expect, it } from 'vitest'
import {
  SessionEventSchema,
  SessionSchema,
  successEnvelope,
  SessionStatusSchema,
  ProjectWorktreesResponseSchema,
  CreateProjectWorktreeRequestSchema,
  CreateProjectWorktreeResponseSchema,
  RemoveProjectWorktreeRequestSchema,
  RemoveProjectWorktreeResponseSchema,
} from './index.js'

describe('protocol schemas', () => {
  it('parses session (schema v2)', () => {
    const result = SessionSchema.parse({
      id: 'sess-1',
      kind: 'pi',
      alive: true,
      pid: 12345,
      title: 'test session',
      status: { label: 'thinking', working: true },
      terminal_cols: 120,
      terminal_rows: 40,
    })

    expect(result.id).toBe('sess-1')
    expect(result.alive).toBe(true)
    expect(result.status?.working).toBe(true)
    expect(result.status?.label).toBe('thinking')
    expect(result.terminal_cols).toBe(120)
    expect(result.terminal_rows).toBe(40)
  })

  it('parses Git layout and keeps legacy absence unknown', () => {
    expect(SessionSchema.parse({ id: 'repo', alive: true, git_layout: 'repository' }).git_layout).toBe('repository')
    expect(SessionSchema.parse({ id: 'worktree', alive: true, git_layout: 'worktree' }).git_layout).toBe('worktree')
    expect(SessionSchema.parse({ id: 'legacy', alive: false }).git_layout).toBeUndefined()
    expect(() => SessionSchema.parse({ id: 'bad', alive: true, git_layout: 'bare' })).toThrow()
  })

  it('parses session with null status', () => {
    const result = SessionSchema.parse({
      id: 'sess-2',
      kind: 'generic',
      alive: false,
      status: null,
    })

    expect(result.status).toBeNull()
    expect(result.alive).toBe(false)
  })

  it('validates session-upsert event', () => {
    const event = SessionEventSchema.parse({
      type: 'session-upsert',
      id: 'sess-1',
      session: {
        id: 'sess-1',
        kind: 'pi',
        alive: true,
        status: { label: 'running', working: true },
      },
    })

    expect(event.type).toBe('session-upsert')
    if (event.type === 'session-upsert') {
      expect(event.session.alive).toBe(true)
    }
  })

  it('validates session-remove event', () => {
    const event = SessionEventSchema.parse({
      type: 'session-remove',
      id: 'sess-1',
    })
    expect(event.type).toBe('session-remove')
  })

  it('builds typed success envelopes', () => {
    const Schema = successEnvelope(SessionStatusSchema)
    const parsed = Schema.parse({ ok: true, data: { label: 'test', working: false } })
    expect(parsed.data.label).toBe('test')
  })

  it('parses project worktree creation payloads', () => {
    expect(CreateProjectWorktreeRequestSchema.parse({ branch: 'fix/auth', base: 'HEAD' })).toEqual({ branch: 'fix/auth', base: 'HEAD' })
    expect(() => CreateProjectWorktreeRequestSchema.parse({ branch: '', path: '/tmp/unsafe' })).toThrow()
    const parsed = CreateProjectWorktreeResponseSchema.parse({
      ok: true,
      data: {
        project_slug: 'gmux',
        worktree: { path: '~/.local/share/gmux/worktrees/fix-auth', branch: 'fix/auth', head: 'abc', primary: false },
      },
    })
    expect(parsed.ok && parsed.data.worktree.branch).toBe('fix/auth')
  })

  it('parses safe project worktree removal payloads', () => {
    expect(RemoveProjectWorktreeRequestSchema.parse({ path: '~/src/gmux-feature' })).toEqual({ path: '~/src/gmux-feature' })
    expect(() => RemoveProjectWorktreeRequestSchema.parse({ path: '', force: true })).toThrow()
    const parsed = RemoveProjectWorktreeResponseSchema.parse({
      ok: true,
      data: { project_slug: 'gmux', removed_path: '~/src/gmux-feature' },
    })
    expect(parsed.ok && parsed.data.removed_path).toBe('~/src/gmux-feature')
  })

  it('parses project worktree inventories', () => {
    const parsed = ProjectWorktreesResponseSchema.parse({
      ok: true,
      data: {
        project_slug: 'gmux',
        primary_path: '~/src/gmux',
        worktrees: [
          { path: '~/src/gmux', branch: 'main', head: 'abc', primary: true },
          { path: '~/.local/share/gmux/worktrees/fix', branch: 'fix/auth', head: 'def', primary: false, locked: true },
        ],
      },
    })
    expect(parsed.ok).toBe(true)
    if (parsed.ok) {
      expect(parsed.data.worktrees[0].primary).toBe(true)
      expect(parsed.data.worktrees[1].locked).toBe(true)
      expect(parsed.data.worktrees[1].detached).toBe(false)
    }
  })
})
