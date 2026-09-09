import { useEffect, useRef, useState } from 'preact/hooks'
import { LaunchButton } from './launcher'
import { sessionsByManagedWorktree } from './projects'
import { viewToPath } from './routing'
import { SheetBackdrop } from './sheet'
import {
  createProjectWorktree,
  ensureProjectWorktrees,
  projectWorktreeInventories,
  projectWorktreeInventoryKey,
  removeProjectWorktree,
  projects,
  sessions,
  tabHref,
} from './store'

export function WorktreeSheet({ slug, peer, onClose }: { slug: string; peer?: string; onClose: () => void }) {
  const key = projectWorktreeInventoryKey(slug, peer)
  const inventory = projectWorktreeInventories.value[key]
  const [branch, setBranch] = useState('')
  const [base, setBase] = useState('HEAD')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmPath, setConfirmPath] = useState('')
  const closeRef = useRef<HTMLButtonElement>(null)
  const worktrees = inventory?.data?.worktrees ?? []
  const sessionsByWorktree = sessionsByManagedWorktree(worktrees, sessions.value, slug, peer)

  useEffect(() => {
    const frame = requestAnimationFrame(() => closeRef.current?.focus())
    return () => cancelAnimationFrame(frame)
  }, [])

  useEffect(() => { void ensureProjectWorktrees(slug, peer) }, [slug, peer])
  useEffect(() => {
    const onEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', onEscape)
    return () => document.removeEventListener('keydown', onEscape)
  }, [onClose])

  const create = async () => {
    if (!branch.trim() || busy) return
    setBusy(true)
    setError('')
    try {
      await createProjectWorktree(slug, branch.trim(), base.trim() || 'HEAD', peer)
      setBranch('')
      setBase('HEAD')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (path: string) => {
    if (confirmPath !== path) {
      setConfirmPath(path)
      return
    }
    setBusy(true)
    setError('')
    try {
      await removeProjectWorktree(slug, path, peer)
      setConfirmPath('')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <SheetBackdrop onClose={onClose}>
      <section class="worktree-sheet" role="dialog" aria-modal="true" aria-label={`Worktrees for ${slug}`}>
        <header class="worktree-sheet-header">
          <div>
            <h2>Worktrees</h2>
            <p>{slug}{peer ? ` on ${peer}` : ''}</p>
          </div>
          <button ref={closeRef} type="button" class="worktree-close" onClick={onClose} aria-label="Close">×</button>
        </header>

        <div class="worktree-list">
          {inventory?.loading && !inventory.data && <p class="worktree-state">Loading checkouts…</p>}
          {inventory?.error && <p class="worktree-error">{inventory.error}</p>}
          {worktrees.map(worktree => {
            const members = sessionsByWorktree.get(worktree.path) ?? []
            const active = members.filter(session => session.status?.active).length
            return (
              <article class="worktree-card" key={worktree.path}>
                <div class="worktree-card-head">
                  <div class="worktree-row-main">
                    <div class="worktree-branch-line">
                      <strong>{worktree.branch || worktree.head?.slice(0, 12) || 'checkout'}</strong>
                      {worktree.primary && <small>Main</small>}
                    </div>
                    <span>{worktree.path}</span>
                    <div class="worktree-stats">
                      {members.length} {members.length === 1 ? 'session' : 'sessions'}
                      {active > 0 && <b> · {active} working</b>}
                    </div>
                  </div>
                  <div class="worktree-card-actions">
                    <LaunchButton cwd={worktree.path} peer={peer} className="worktree-launch" />
                    {!worktree.primary && (
                      <button type="button" class={`worktree-remove${confirmPath === worktree.path ? ' confirm' : ''}`} disabled={busy}
                        onClick={() => void remove(worktree.path)}>
                        {confirmPath === worktree.path ? 'Remove?' : 'Remove'}
                      </button>
                    )}
                  </div>
                </div>
                <div class="worktree-session-list">
                  {members.slice(0, 3).map(session => {
                    const path = viewToPath({ kind: 'session', sessionId: session.id }, projects.value, sessions.value)
                    return (
                      <a class="worktree-session" key={session.id} href={path ? tabHref(path) : undefined} onClick={onClose}>
                        <span class={`worktree-session-dot${session.status?.active ? ' active' : session.unread ? ' unread' : ''}`} />
                        <span class="worktree-session-title">{session.title}</span>
                        {session.parent_session_id && <small title="Member of a session family">↳ family</small>}
                      </a>
                    )
                  })}
                  {members.length === 0 && <div class="worktree-session-empty">No sessions in this checkout</div>}
                  {members.length > 3 && <div class="worktree-session-more">+{members.length - 3} more sessions</div>}
                </div>
              </article>
            )
          })}
          {inventory?.data && inventory.data.worktrees.length === 0 && <p class="worktree-state">This project is not a Git repository.</p>}
        </div>

        <form class="worktree-create" onSubmit={event => { event.preventDefault(); void create() }}>
          <h3>New linked worktree</h3>
          <label>Branch<input value={branch} onInput={event => setBranch(event.currentTarget.value)} placeholder="fix/login" autocomplete="off" /></label>
          <label>Base<input value={base} onInput={event => setBase(event.currentTarget.value)} placeholder="HEAD" autocomplete="off" /></label>
          {error && <p class="worktree-error">{error}</p>}
          <button type="submit" disabled={busy || !branch.trim()}>{busy ? 'Working…' : 'Create worktree'}</button>
        </form>
      </section>
    </SheetBackdrop>
  )
}
