/**
 * Sidebar: project folders, session items, and the navigation shell.
 *
 * Reads shared state directly from the store (signals). Only action
 * callbacks and the mobile open/close toggle are passed as props.
 */

import { useState, useEffect, useRef } from 'preact/hooks'
import { sessionPath } from './routing'
import { canCreateManagedWorktree, groupSessionsByCheckout, reorderKeysForFolder, type CheckoutGroup } from './projects'
import { LaunchButton } from './launcher'
import { useArrivalPulse } from './use-arrival-pulse'
import {
  folders, selectedId, currentProjectKey,
  activityMap, projects, connState, worldLoaded,
  updateProjects, reorderSessions,
  peerStatusByName, isSessionUnavailable, localPeerNames, sessionDotState, aggregateSessionDotState,
  unreadCount, localHostLabel, unresolvedHosts, duplicateSessionFiles,
  vsCodeServerUrl, vsCodeServerHomeDir,
  projectWorktreeInventories, projectWorktreeInventoryKey, ensureProjectWorktrees, createProjectWorktree, removeProjectWorktree,
  type DotState,
} from './store'
import { HostSuffix } from './host-suffix'
import { buildVSCodeServerUrl } from './vscode-server'
import { fileBrowserPath, projectFileBrowserPath } from './file-browser'
import { SheetBackdrop } from './sheet'
import type { Session, Folder } from './types'

// ── Types ──

export type NotifPermission = 'default' | 'granted' | 'denied' | 'unavailable'

// Re-export DotState so existing imports keep working.
export type { DotState }

// ── Helpers ──

/** Determine the dot indicator state for a session. */

const bellStroke = { fill: 'none', stroke: 'currentColor', 'stroke-width': '1.4', 'stroke-linecap': 'round' as const, 'stroke-linejoin': 'round' as const }

export const IconBell = ({ muted }: { muted?: boolean }) => (
  <svg viewBox="0 0 14 14" width="14" height="14" {...bellStroke} style={{ opacity: muted ? 0.4 : 1 }}>
    <path d="M7 2a4 4 0 0 1 4 4v2.5l1 1.5H2l1-1.5V6a4 4 0 0 1 4-4Z"/>
    <path d="M5.5 11.5a1.5 1.5 0 0 0 3 0" stroke-width="1.2"/>
  </svg>
)

export const IconSettings = () => (
  <svg viewBox="0 0 16 16" width="15" height="15" {...bellStroke}>
    <path d="M2 4.5h7M12 4.5h2M2 11.5h2M7 11.5h7"/>
    <circle cx="10.5" cy="4.5" r="1.7"/>
    <circle cx="5.5" cy="11.5" r="1.7"/>
  </svg>
)

export const IconRefresh = () => (
  <svg viewBox="0 0 16 16" width="15" height="15" {...bellStroke}>
    <path d="M13 5.2A5.2 5.2 0 0 0 3.4 4" />
    <path d="M13 2.5v2.7h-2.7" />
    <path d="M3 10.8A5.2 5.2 0 0 0 12.6 12" />
    <path d="M3 13.5v-2.7h2.7" />
  </svg>
)

export const IconVSCode = () => (
  <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
    <path d="M11.5 2.5 5 7.8l6.5 5.7 2-1V3.5l-2-1Z" />
    <path d="M5 7.8 2.8 5.7 2 6.3v3l.8.6L5 7.8Z" />
  </svg>
)

export const IconFiles = () => (
  <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
    <path d="M2.5 4.5h4l1.2 1.4h5.8v6.6a1 1 0 0 1-1 1h-10a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1Z" />
    <path d="M2.5 4.5V3.8a1 1 0 0 1 1-1h2.7l1.1 1.2" />
  </svg>
)

// ── Drag helpers ──

/** True on devices with a pointer (mouse/trackpad). Touch-only devices
 *  don't support the HTML5 drag API and setting draggable on them
 *  interferes with scroll. */
const canDrag = typeof matchMedia !== 'undefined' && matchMedia('(hover: hover)').matches

interface DragState {
  /** Index of the item being dragged (in the original array). */
  from: number
  /** Current visual insertion target. */
  over: number
}

// ── Components ──

/** Container icon for a devcontainer session inside a mixed-host
 *  folder. Replaces the per-row PeerLabel pill (which didn't tell
 *  anyone "this runs in a container" anyway).
 *
 *  Reachability is guaranteed by the `buildProjectFolders`
 *  bucketing: any session with `.peer` set inside a folder where
 *  `folder.peer === undefined` is a Local peer (parent-owned
 *  devcontainer). Pinned by `projects.test.ts`
 *  ("non-Local peer sessions never land in a locally-owned
 *  folder"). Peer-owned folders are single-host by construction,
 *  so showHostMarker never fires there.
 */
function DevcontainerMarker({ peer }: { peer: string }) {
  return (
    <svg class="session-container-icon" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
      <title>{`devcontainer: ${peer}`}</title>
      <rect x="1.5" y="3.5" width="9" height="6" rx="0.5" />
      <path d="M4 3.5v6 M6 3.5v6 M8 3.5v6" />
    </svg>
  )
}

function reorder<T>(arr: T[], from: number, to: number): T[] {
  const next = [...arr]
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

function folderWorkspacePath(folder: Folder, visible: Session[]): string {
  if (folder.launchCwd?.trim()) return folder.launchCwd
  const session = visible.find(s => s.alive && (s.workspace_root?.trim() || s.cwd?.trim()))
    ?? visible.find(s => s.workspace_root?.trim() || s.cwd?.trim())
  return session?.workspace_root?.trim() || session?.cwd?.trim() || ''
}

function FilesButton({ session, projectSlug, onClick }: { session?: Session; projectSlug?: string; onClick?: () => void }) {
  const root = session ? session.workspace_root || session.cwd : projectSlug || 'project files'
  return (
    <a
      class="folder-file-btn"
      href={session ? fileBrowserPath(session.id) : projectFileBrowserPath(projectSlug || '')}
      title={`Browse ${root}`}
      aria-label={`Browse ${root}`}
      onClick={(e) => {
        e.stopPropagation()
        onClick?.()
      }}
    >
      <IconFiles />
    </a>
  )
}

function VSCodeServerButton({ href, workspacePath }: { href: string; workspacePath: string }) {
  return (
    <button
      class="folder-vscode-btn"
      type="button"
      title={`Open ${workspacePath} in VS Code Server`}
      aria-label={`Open ${workspacePath} in VS Code Server`}
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        window.open(href, '_blank', 'noopener,noreferrer')
      }}
    >
      <IconVSCode />
    </button>
  )
}

function SessionItem({
  session,
  href,
  selected,
  resuming,
  dotState: rawDotState,
  dragging,
  dropTarget,
  unavailable,
  showHostMarker,
  onClose,
  onClick,
  onDragStart,
  onDragOver,
  onDragEnd,
}: {
  session: Session
  href: string
  selected: boolean
  resuming?: boolean
  dotState: DotState
  dragging?: boolean
  dropTarget?: boolean
  /** Session lives on a peer we can't reach right now. */
  unavailable?: boolean
  /** Folder spans multiple hosts; render this session's host marker. */
  showHostMarker?: boolean
  onClose?: () => void
  /** Extra side-effects on click (e.g. close mobile sidebar). */
  onClick?: () => void
  onDragStart?: () => void
  onDragOver?: () => void
  onDragEnd?: () => void
}) {
  const effectiveDotState = resuming ? 'working' : rawDotState
  // Nothing is "unread" if you're already looking at it.
  const dotState = (selected && (effectiveDotState === 'error' || effectiveDotState === 'unread')) ? 'none' : effectiveDotState
  const arrival = useArrivalPulse(dotState)
  const sleeping = !session.alive && session.resumable
  // Same conversation file live in another runner (ADR 0011 N:1).
  const duplicateOpen = !!session.session_file && duplicateSessionFiles.value.has(session.session_file)

  const cls = [
    'session-item',
    selected ? 'selected' : '',
    dragging ? 'session-dragging' : '',
    dropTarget ? 'session-drop-target' : '',
    unavailable ? 'unavailable' : '',
    onClose ? 'has-close' : '',
  ].filter(Boolean).join(' ')

  return (
    <a
      class={cls}
      href={href}
      draggable={canDrag && !!onDragStart}
      onClick={() => {
        onClick?.()
      }}
      onAuxClick={(e) => { if (e.button === 1 && onClose) { e.preventDefault(); onClose() } }}
      onDragStart={(e) => {
        e.dataTransfer!.effectAllowed = 'move'
        e.dataTransfer!.setData('text/plain', '')
        onDragStart?.()
      }}
      onDragOver={(e) => { e.preventDefault(); e.dataTransfer!.dropEffect = 'move'; onDragOver?.() }}
      onDrop={(e) => { e.preventDefault(); onDragEnd?.() }}
      onDragEnd={onDragEnd}
    >
      {unavailable
        ? <svg class="session-unavailable-icon" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><title>Peer unavailable</title><path d="M2 2 L10 10 M10 2 L2 10" /></svg>
        : sleeping
        ? <svg class="session-sleep-icon" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><title>Resumable</title><path d="M7 1h4l-4 4h4" /><path d="M1 5h5l-5 6h5" /></svg>
        : <span class={`session-dot-indicator ${dotState}${arrival ? ` ${arrival}` : ''}`} />
      }
      {showHostMarker && session.peer && <DevcontainerMarker peer={session.peer} />}
      <div class="session-content">
        <div class="session-title-row">
          <span class="session-title">{session.title}</span>
        </div>
        {(session.status?.label || duplicateOpen) && (
          <div class="session-meta">
            {session.status?.label && <span class="session-status-label">{session.status.label}</span>}
            {duplicateOpen && (
              <span class="session-dup-warning" title="This conversation is open in more than one tab">⚠ open elsewhere</span>
            )}
          </div>
        )}
      </div>
      {onClose && (
        <button
          class="session-close-btn"
          onClick={(e) => { e.stopPropagation(); e.preventDefault(); onClose() }}
          title={session.alive ? 'Kill session' : 'Dismiss'}
          aria-label={session.alive ? 'Kill session' : 'Dismiss session'}
        >
          ×
        </button>
      )}
    </a>
  )
}

function CheckoutSection({
  group,
  folder,
  selId,
  resumingId,
  am,
  peerStatus,
  mixedHosts,
  onCloseSession,
  onClick,
  onReorder,
}: {
  group: CheckoutGroup
  folder: Folder
  selId: string | null
  resumingId: string | null
  am: ReadonlyMap<string, 'active' | 'fading'>
  peerStatus: ReadonlyMap<string, string>
  mixedHosts: boolean
  onCloseSession: (session: Session) => void
  onClick?: () => void
  onReorder: (from: number, to: number) => void
}) {
  const [drag, setDrag] = useState<DragState | null>(null)
  const [expanded, setExpanded] = useState(true)
  const [removeOpen, setRemoveOpen] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [removeError, setRemoveError] = useState('')
  const removeTriggerRef = useRef<HTMLButtonElement>(null)
  const removeDialogRef = useRef<HTMLDivElement>(null)
  const displayItems = drag ? reorder(group.sessions, drag.from, drag.over) : group.sessions
  const collapsedDot = aggregateSessionDotState(group.sessions, am, {
    selectedId: selId,
    resumingId,
    peerStatus,
  })
  const collapsedArrival = useArrivalPulse(expanded ? 'none' : collapsedDot)
  const canLaunch = !group.fallback && group.path !== ''
  const canRemove = !!group.worktree && !group.primary && !group.fallback
  const handleDragEnd = () => {
    if (drag && drag.from !== drag.over) onReorder(drag.from, drag.over)
    setDrag(null)
  }
  const closeRemove = () => {
    if (removing) return
    setRemoveOpen(false)
    setRemoveError('')
    queueMicrotask(() => removeTriggerRef.current?.focus())
  }
  const confirmRemove = async () => {
    setRemoving(true)
    setRemoveError('')
    try {
      await removeProjectWorktree(folder.slug, group.path, folder.peer)
      setRemoveOpen(false)
    } catch (err) {
      setRemoveError(err instanceof Error ? err.message : String(err))
    } finally {
      setRemoving(false)
    }
  }

  useEffect(() => {
    if (selId && group.sessions.some(session => session.id === selId)) setExpanded(true)
  }, [selId, group.key])

  useEffect(() => {
    if (!removeOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeRemove()
        return
      }
      if (event.key !== 'Tab') return
      const focusable = [...(removeDialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), [href], input:not(:disabled), [tabindex]:not([tabindex="-1"])') ?? [])]
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [removeOpen, removing])

  return (
    <div class={`checkout-group${group.primary ? ' primary' : ''}${group.fallback ? ' fallback' : ''}`}>
      <div class="checkout-header" title={group.path || group.label}>
        <button
          type="button"
          class="checkout-fold-btn"
          aria-expanded={expanded}
          aria-label={`${expanded ? 'Collapse' : 'Expand'} ${group.label}${!expanded && collapsedDot !== 'none' ? ', session needs attention' : ''}`}
          onClick={() => setExpanded(value => !value)}
        >
          <span class={`checkout-chevron${expanded ? ' expanded' : ''}`} aria-hidden="true">›</span>
          <span class="checkout-tree-mark" aria-hidden="true">↳</span>
          <span class="checkout-name">{group.label}</span>
          {group.primary && <span class="checkout-primary-label">default</span>}
          {group.worktree?.locked && <span class="checkout-state-label">locked</span>}
          {!expanded && group.sessions.length > 0 && <span class="checkout-session-count">{group.sessions.length}</span>}
          {!expanded && collapsedDot !== 'none' && (
            <span
              class={`session-dot-indicator ${collapsedDot}${collapsedArrival ? ` ${collapsedArrival}` : ''}`}
              title="A session in this checkout needs attention"
              aria-hidden="true"
            />
          )}
        </button>
        <div class="checkout-actions">
          {canLaunch && (
            <LaunchButton
              cwd={group.path}
              peer={folder.peer}
              className="checkout-launch-btn"
            />
          )}
          {canRemove && (
            <button
              type="button"
              ref={removeTriggerRef}
              class="checkout-remove-btn"
              aria-label={`Remove worktree ${group.label}`}
              title="Remove worktree"
              onClick={() => { setRemoveError(''); setRemoveOpen(true) }}
            >
              <span aria-hidden="true">×</span>
            </button>
          )}
        </div>
      </div>
      {expanded && (
        <div class="checkout-sessions">
          {displayItems.map((s, i) => (
            <SessionItem
              key={s.id}
              session={s}
              href={sessionPath(folder.slug, s, folder.peer)}
              selected={selId === s.id}
              resuming={resumingId === s.id}
              dotState={sessionDotState(s, am)}
              unavailable={isSessionUnavailable(s, peerStatus)}
              showHostMarker={mixedHosts}
              dragging={drag !== null && s.id === group.sessions[drag.from]?.id}
              dropTarget={drag !== null && drag.over === i && drag.from !== i}
              onClose={() => onCloseSession(s)}
              onClick={onClick}
              onDragStart={() => setDrag({ from: i, over: i })}
              onDragOver={() => setDrag(prev => prev ? { ...prev, over: i } : null)}
              onDragEnd={handleDragEnd}
            />
          ))}
        </div>
      )}
      {removeOpen && (
        <SheetBackdrop onClose={closeRemove} blurActiveElement={false}>
          <div ref={removeDialogRef} class="modal-panel worktree-remove-sheet" role="alertdialog" aria-modal="true" aria-labelledby="worktree-remove-title">
            <h2 id="worktree-remove-title">Remove worktree?</h2>
            <p><strong>{group.label}</strong></p>
            <code>{group.path}</code>
            <p>The checkout directory will be removed. Its Git branch will remain.</p>
            <p class="worktree-remove-safety">Removal is blocked if it has changes or ignored files, is locked, or has a live or resumable session.</p>
            {removeError && <div class="worktree-remove-error" role="alert">{removeError}</div>}
            <div class="worktree-remove-buttons">
              <button type="button" class="sheet-btn sheet-btn-quiet" aria-disabled={removing} autoFocus onClick={closeRemove}>Cancel</button>
              <button type="button" class="sheet-btn worktree-remove-confirm" disabled={removing} onClick={() => void confirmRemove()}>
                {removing ? 'Removing…' : 'Remove worktree'}
              </button>
            </div>
          </div>
        </SheetBackdrop>
      )}
    </div>
  )
}

function NewWorktreeSheet({ folder, onClose, restoreTriggerFocus }: { folder: Folder; onClose: () => void; restoreTriggerFocus: boolean }) {
  const [branch, setBranch] = useState('')
  const [base, setBase] = useState('HEAD')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const panelRef = useRef<HTMLFormElement>(null)
  const branchInputRef = useRef<HTMLInputElement>(null)

  const close = () => {
    if (!submitting) onClose()
  }
  const submit = async () => {
    const normalizedBranch = branch.trim()
    const normalizedBase = base.trim() || 'HEAD'
    if (!normalizedBranch) {
      setError('Branch name is required.')
      branchInputRef.current?.focus()
      return
    }
    setSubmitting(true)
    setError('')
    try {
      await createProjectWorktree(folder.slug, normalizedBranch, normalizedBase, folder.peer)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  useEffect(() => () => {
    if (restoreTriggerFocus) {
      queueMicrotask(() => [...document.querySelectorAll<HTMLElement>('[data-new-worktree-key]')].find(element => element.dataset.newWorktreeKey === folder.key)?.focus())
    }
  }, [folder.key, restoreTriggerFocus])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        close()
        return
      }
      if (event.key !== 'Tab') return
      const focusable = [...(panelRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), summary, [tabindex]:not([tabindex="-1"])') ?? [])]
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [submitting])

  return (
    <SheetBackdrop onClose={close} blurActiveElement={false}>
      <form
        ref={panelRef}
        class="modal-panel worktree-create-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="worktree-create-title"
        aria-busy={submitting}
        onSubmit={(event) => { event.preventDefault(); void submit() }}
      >
        <h2 id="worktree-create-title">New worktree</h2>
        <p class="worktree-create-project">Create a linked checkout for <strong>{folder.name}</strong>.</p>
        <label class="worktree-create-field">
          <span>Branch</span>
          <input
            ref={branchInputRef}
            value={branch}
            disabled={submitting}
            autoFocus
            autoCapitalize="none"
            autoCorrect="off"
            spellcheck={false}
            placeholder="fix/login"
            aria-invalid={!!error || undefined}
            aria-describedby="worktree-create-help"
            onInput={(event) => setBranch(event.currentTarget.value)}
          />
        </label>
        <details class="worktree-create-advanced">
          <summary>Advanced</summary>
          <label class="worktree-create-field">
            <span>Base</span>
            <input
              value={base}
              disabled={submitting}
              autoCapitalize="none"
              autoCorrect="off"
              spellcheck={false}
              placeholder="HEAD"
              onInput={(event) => setBase(event.currentTarget.value)}
            />
          </label>
        </details>
        <div id="worktree-create-help" class="worktree-create-help">
          gmux chooses a managed path. Uncommitted and untracked files from the source checkout are not copied.
        </div>
        {error && <div class="worktree-remove-error" role="alert">{error}</div>}
        <div class="worktree-remove-buttons">
          <button type="button" class="sheet-btn sheet-btn-quiet" aria-disabled={submitting} onClick={close}>Cancel</button>
          <button type="submit" class="sheet-btn sheet-btn-primary worktree-create-submit" disabled={submitting || !branch.trim()}>
            {submitting ? 'Creating…' : 'Create worktree'}
          </button>
        </div>
      </form>
    </SheetBackdrop>
  )
}

function FolderGroup({
  folder,
  selId,
  currentKey,
  resumingId,
  am,
  peerStatus,
  onCloseSession,
  onClick,
  onNewWorktree,
}: {
  folder: Folder
  selId: string | null
  currentKey: string | null
  resumingId: string | null
  am: ReadonlyMap<string, 'active' | 'fading'>
  peerStatus: ReadonlyMap<string, string>
  onCloseSession: (session: Session) => void
  onClick?: () => void
  onNewWorktree: () => void
}) {
  const ownerStatus = folder.peer ? peerStatus.get(folder.peer) : 'local'
  useEffect(() => {
    if (!folder.unresolved && !folder.missing && (ownerStatus === 'local' || ownerStatus === 'connected')) {
      void ensureProjectWorktrees(folder.slug, folder.peer, ownerStatus === 'connected')
    }
  }, [folder.slug, folder.peer, folder.unresolved, folder.missing, ownerStatus])

  const inventoryKey = projectWorktreeInventoryKey(folder.slug, folder.peer)
  const inventory = projectWorktreeInventories.value[inventoryKey]
  const ownerReachable = ownerStatus === 'local' || ownerStatus === 'connected'
  const canCreateWorktree = ownerReachable && !folder.missing && !folder.unresolved && !inventory?.loading && !inventory?.error && canCreateManagedWorktree(inventory?.data?.worktrees)
  const checkoutGroups: CheckoutGroup[] = inventory?.error
    ? [{
        key: 'inventory-unavailable',
        path: folder.launchCwd ?? '',
        label: 'Sessions',
        primary: false,
        sessions: folder.sessions.filter(s => s.alive || s.resumable),
        fallback: true,
      }]
    : groupSessionsByCheckout(folder, inventory?.data?.worktrees, inventory?.data?.primary_path)
  const visible = checkoutGroups.flatMap(group => group.sessions)
  const isCurrent = currentKey === folder.key
  const href = folder.peer ? `/@${folder.peer}/${folder.slug}` : `/${folder.slug}`
  // Folder spans multiple hosts iff its sessions don't all share the
  // same .peer value. In practice this is the devcontainer case.
  const folderPeers = new Set(visible.map(s => s.peer ?? ''))
  const mixedHosts = folderPeers.size > 1
  const workspacePath = folderWorkspacePath(folder, visible)
  const codeHref = buildVSCodeServerUrl(vsCodeServerUrl.value, workspacePath, vsCodeServerHomeDir.value)
  const fileSession = visible.find(s => s.workspace_root?.trim() || s.cwd?.trim())

  const handleCheckoutReorder = (group: CheckoutGroup, from: number, to: number) => {
    const reordered = reorder(group.sessions, from, to)
    const allVisible = checkoutGroups.flatMap(item => item.key === group.key ? reordered : item.sessions)
    const visibleKeys = reorderKeysForFolder(
      allVisible,
      folder.peer,
      (name) => localPeerNames.value.has(name),
    )
    if (visibleKeys.length > 0) {
      reorderSessions(folder.slug, visibleKeys, folder.peer)
    }
  }

  return (
    <div class="folder">
      <div class="folder-header">
        <a
          class={`folder-name${isCurrent ? ' current' : ''}${folder.missing ? ' missing' : ''}${folder.unresolved ? ' unresolved' : ''}`}
          href={href}
          title={folder.unresolved
            ? `Host “${folder.peer}” isn't a connected or manually-added host — it may have been renamed or removed. Open Settings → Hosts to remap or remove it.`
            : folder.missing
            ? `${folder.name} no longer exists on ${folder.peer}; remove via the home page`
            : `Open ${folder.name} hub`}
          onClick={onClick}
        >
          {folder.name}
          <HostSuffix peer={folder.peer ?? localHostLabel.value} local={!folder.peer} />
          {folder.missing && <span class="folder-missing-icon" title="Project missing on peer">?</span>}
          {folder.unresolved && (
            <span class="folder-unresolved-icon" title="Host not found — fix in Settings → Hosts">!</span>
          )}
        </a>
        {!folder.unresolved && (
          <div class="folder-header-actions">
            {(fileSession || (!folder.peer && folder.launchCwd)) && <FilesButton session={fileSession} projectSlug={folder.slug} onClick={onClick} />}
            {codeHref && <VSCodeServerButton href={codeHref} workspacePath={workspacePath} />}
            <LaunchButton
              sessions={folder.sessions}
              selectedId={selId}
              fallbackCwd={folder.launchCwd ?? ''}
              peer={folder.peer}
              className="folder-launch-btn"
              footerAction={canCreateWorktree ? {
                label: 'New worktree',
                onSelect: onNewWorktree,
                triggerKey: folder.key,
              } : undefined}
            />
          </div>
        )}
      </div>
      <div class="folder-checkouts" aria-busy={inventory?.loading || undefined}>
        {inventory?.error && (
          <button
            type="button"
            class="checkout-inventory-error"
            title={inventory.error}
            onClick={() => void ensureProjectWorktrees(folder.slug, folder.peer, true)}
          >
            Worktrees unavailable · Retry
          </button>
        )}
        {checkoutGroups.map(group => (
          <CheckoutSection
            key={group.key}
            group={group}
            folder={folder}
            selId={selId}
            resumingId={resumingId}
            am={am}
            peerStatus={peerStatus}
            mixedHosts={mixedHosts}
            onCloseSession={onCloseSession}
            onClick={onClick}
            onReorder={(from, to) => handleCheckoutReorder(group, from, to)}
          />
        ))}
      </div>
    </div>
  )
}

export function Sidebar({
  resumingId,
  onCloseSession,
  onOpenSettings,
  newWorktreeKey,
  onOpenNewWorktree,
  onCloseNewWorktree,
  open,
  onClose,
}: {
  resumingId: string | null
  onCloseSession: (session: Session) => void
  onOpenSettings: () => void
  newWorktreeKey?: string
  onOpenNewWorktree: (key: string) => void
  onCloseNewWorktree: () => void
  open: boolean
  onClose: () => void
}) {
  // Read signals; component re-renders only when these values change.
  const foldersVal = folders.value
  const projectsVal = projects.value
  const selId = selectedId.value
  const curKey = currentProjectKey.value
  const am = activityMap.value
  const peerStatus = peerStatusByName.value

  // Waiting indicator on the logo: mirrors the mobile hamburger badge so
  // the always-visible brand mark doubles as a "a session elsewhere is
  // waiting on you" cue. Only the waiting (unread) state is surfaced —
  // working/active are deliberately omitted. unreadCount excludes the
  // selected session (see store.ts); its value also drives the re-blink
  // when an additional session enters the waiting state.
  const waitingCount = unreadCount.value
  const waiting = waitingCount > 0
  // A reference points at a host that's in no roster bucket (renamed /
  // removed): flag the gear so the user knows where the fix lives. (refs #270)
  const hasUnresolved = unresolvedHosts.value.length > 0
  const bgArrival = useArrivalPulse(waiting ? 'unread' : 'none', waitingCount)
  const requestedWorktreeFolder = newWorktreeKey ? foldersVal.find(folder => folder.key === newWorktreeKey) : undefined
  const requestedOwnerStatus = requestedWorktreeFolder?.peer ? peerStatus.get(requestedWorktreeFolder.peer) : 'local'
  const requestedInventory = requestedWorktreeFolder
    ? projectWorktreeInventories.value[projectWorktreeInventoryKey(requestedWorktreeFolder.slug, requestedWorktreeFolder.peer)]
    : undefined
  const requestedEligible = !!requestedWorktreeFolder
    && !requestedWorktreeFolder.missing
    && !requestedWorktreeFolder.unresolved
    && (requestedOwnerStatus === 'local' || requestedOwnerStatus === 'connected')
    && !requestedInventory?.error
    && canCreateManagedWorktree(requestedInventory?.data?.worktrees)
  const newWorktreeFolder = requestedEligible ? requestedWorktreeFolder : undefined

  useEffect(() => {
    if (!newWorktreeKey) return
    if (!requestedWorktreeFolder) {
      if (worldLoaded.value) onCloseNewWorktree()
      return
    }
    const permanentlyInvalid = requestedWorktreeFolder.missing
      || requestedWorktreeFolder.unresolved
      || (requestedOwnerStatus !== 'local' && requestedOwnerStatus !== 'connected')
      || !!requestedInventory?.error
      || (!!requestedInventory?.data && !canCreateManagedWorktree(requestedInventory.data.worktrees))
    if (permanentlyInvalid) onCloseNewWorktree()
  }, [newWorktreeKey, requestedWorktreeFolder, requestedOwnerStatus, requestedInventory?.error, requestedInventory?.data, worldLoaded.value, onCloseNewWorktree])

  const totalVisible = foldersVal.reduce(
    (n, f) => n + f.sessions.filter(s => s.alive || s.resumable).length, 0,
  )
  const connected = connState.value === 'connected'
  const hasProjects = projectsVal.length > 0
  const isOnlyHomeProject = projectsVal.length === 1
    && projectsVal[0].slug === 'home'
    && !!projectsVal[0].match?.some(r => r.path === '~' && r.exact)

  const seedHomeProject = async () => {
    if (projects.value.length === 0) {
      await updateProjects([{ slug: 'home', match: [{ path: '~', exact: true }] }])
    }
  }

  return (
    <>
      <div class={`sidebar-overlay ${open ? 'visible' : ''}`} onClick={onClose} />
      <aside class={`sidebar ${open ? 'open' : ''}`}>
        <div class="sidebar-header">
          <a
            class={`sidebar-logo${waiting ? ' bg-waiting' : ''}${bgArrival ? ` bg-${bgArrival}` : ''}`}
            href="/"
            onClick={onClose}
          >gmux</a>
          <button
            class="sidebar-refresh-btn"
            onClick={() => location.reload()}
            aria-label="Refresh app"
            title="Refresh app"
          >
            <IconRefresh />
          </button>
          <button
            class="sidebar-settings-btn"
            onClick={onOpenSettings}
            aria-label={hasUnresolved ? 'Settings (a referenced host needs attention)' : 'Settings'}
            title={hasUnresolved ? 'A referenced host was not found — open Settings → Hosts' : 'Settings'}
          >
            <IconSettings />
            {hasUnresolved && <span class="settings-attention-pip" aria-hidden="true" />}
          </button>
        </div>
        <div class="sidebar-scroll">
          {foldersVal.map(f => (
            <FolderGroup
              key={f.key}
              folder={f}
              selId={selId}
              currentKey={curKey}
              resumingId={resumingId}
              am={am}
              peerStatus={peerStatus}
              onCloseSession={onCloseSession}
              onClick={onClose}
              onNewWorktree={() => onOpenNewWorktree(f.key)}
            />
          ))}
          {connected && !hasProjects && (
            <div class="sidebar-empty-launch">
              <LaunchButton
                className="sidebar-launch-btn"
                beforeLaunch={seedHomeProject}
                onLaunch={onClose}
              />
            </div>
          )}
          {connected && totalVisible === 0 && !hasProjects && (
            <div class="sidebar-hint">
              Click <strong>+</strong> to start your first session.
            </div>
          )}
          {connected && isOnlyHomeProject && totalVisible > 0 && (
            <div class="sidebar-hint">
              <button class="sidebar-hint-link" onClick={onOpenSettings}>
                Add a project
              </button> to organize sessions by repo.
            </div>
          )}
        </div>
      </aside>
      {newWorktreeFolder && (
        <NewWorktreeSheet
          key={newWorktreeFolder.key}
          folder={newWorktreeFolder}
          onClose={onCloseNewWorktree}
          restoreTriggerFocus={open || !matchMedia('(pointer: coarse)').matches}
        />
      )}
    </>
  )
}
