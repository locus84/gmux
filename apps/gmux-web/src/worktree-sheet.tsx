import { useEffect, useState } from 'preact/hooks'
import { LaunchButton } from './launcher'
import { SheetBackdrop } from './sheet'
import {
  createProjectWorktree,
  ensureProjectWorktrees,
  projectWorktreeInventories,
  projectWorktreeInventoryKey,
  removeProjectWorktree,
} from './store'

export function WorktreeSheet({ slug, peer, onClose }: { slug: string; peer?: string; onClose: () => void }) {
  const key = projectWorktreeInventoryKey(slug, peer)
  const inventory = projectWorktreeInventories.value[key]
  const [branch, setBranch] = useState('')
  const [base, setBase] = useState('HEAD')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmPath, setConfirmPath] = useState('')

  useEffect(() => { void ensureProjectWorktrees(slug, peer) }, [slug, peer])
  useEffect(() => {
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
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
          <button type="button" class="worktree-close" onClick={onClose} aria-label="Close">×</button>
        </header>

        <div class="worktree-list">
          {inventory?.loading && !inventory.data && <p class="worktree-state">Loading checkouts…</p>}
          {inventory?.error && <p class="worktree-error">{inventory.error}</p>}
          {inventory?.data?.worktrees.map(worktree => (
            <div class="worktree-row" key={worktree.path}>
              <div class="worktree-row-main">
                <strong>{worktree.branch || worktree.head?.slice(0, 12) || 'checkout'}</strong>
                <span>{worktree.path}</span>
                {worktree.primary && <small>Primary checkout</small>}
              </div>
              <LaunchButton cwd={worktree.path} peer={peer} className="worktree-launch" />
              {!worktree.primary && (
                <button type="button" class={`worktree-remove${confirmPath === worktree.path ? ' confirm' : ''}`} disabled={busy}
                  onClick={() => void remove(worktree.path)}>
                  {confirmPath === worktree.path ? 'Remove?' : 'Remove'}
                </button>
              )}
            </div>
          ))}
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
