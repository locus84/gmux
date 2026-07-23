// Launcher UI.
//
// Pure functions for launcher resolution + the LaunchButton component.
// Launcher definitions come from the store's health signal (populated
// from /v1/health). The component reads them reactively.

import { createPortal } from 'preact/compat'
import { useState, useRef, useEffect, useLayoutEffect, useMemo } from 'preact/hooks'
import type { Session, LauncherDef, PeerInfo } from './types'
import { launchers as launchersSignal, defaultLauncher as defaultLauncherSignal, peers as peersSignal, launchSession } from './store'

/** Resolved launch target: where the session will be created. */
export interface LaunchTarget {
  peer?: string
  cwd: string
}

/** Return launchers for a specific peer, falling back to local config. */
export function launchersForPeer(
  localLaunchers: LauncherDef[],
  localDefault: string,
  peers: PeerInfo[],
  peer: string | undefined,
): { default_launcher: string; launchers: LauncherDef[] } {
  if (peer) {
    const p = peers.find(pp => pp.name === peer)
    if (p?.launchers?.length) {
      return { default_launcher: p.default_launcher ?? localDefault, launchers: p.launchers }
    }
  }
  return { default_launcher: localDefault, launchers: localLaunchers }
}

// ── Target resolution ──

/**
 * Derive the launch target from the user's current context.
 *
 * Priority:
 *  1. The currently selected session (if it belongs to this project)
 *  2. The most recently created alive session in the project
 *  3. The project's configured fallback path (local)
 */
export function resolveTarget(
  sessions: Session[],
  selectedId: string | null,
  fallbackCwd: string,
): LaunchTarget {
  // 1. Selected session in this project?
  if (selectedId) {
    const selected = sessions.find(s => s.id === selectedId)
    if (selected) return { peer: selected.peer, cwd: selected.cwd }
  }

  // 2. Most recently created alive session?
  const alive = sessions
    .filter(s => s.alive)
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  if (alive.length > 0) return { peer: alive[0].peer, cwd: alive[0].cwd }

  // 3. Fallback to the project's configured path, local.
  return { cwd: fallbackCwd }
}

/** Format a target for display: "~/dev/gmux" or "laptop: ~/dev/gmux". */
export function formatTarget(target: LaunchTarget): string {
  const shortCwd = target.cwd.replace(/^\/home\/[^/]+/, '~')
  if (target.peer) return `${target.peer}: ${shortCwd}`
  return shortCwd
}

export interface LauncherMenuViewport {
  left: number
  top: number
  width: number
  height: number
}

export interface LauncherMenuPosition {
  left: number
  top: number
  maxWidth: number
  maxHeight: number
}

export function launcherMenuPosition(
  anchor: { left: number; top: number; right: number },
  menu: { width: number; height: number },
  viewport: LauncherMenuViewport,
  targetOffset: number,
  gutter = 8,
  gap = 4,
): LauncherMenuPosition {
  const maxWidth = Math.max(0, viewport.width - gutter * 2)
  const maxHeight = Math.max(0, viewport.height - gutter * 2)
  const width = Math.min(menu.width, maxWidth)
  const height = Math.min(menu.height, maxHeight)
  const minLeft = viewport.left + gutter
  const maxLeft = viewport.left + viewport.width - gutter - width
  const minTop = viewport.top + gutter
  const maxTop = viewport.top + viewport.height - gutter - height

  let top = anchor.top - 4 - targetOffset
  if (top + height > viewport.top + viewport.height - gutter) {
    top = anchor.top - height - gap
  }

  return {
    left: Math.min(maxLeft, Math.max(minLeft, anchor.right - width)),
    top: Math.min(maxTop, Math.max(minTop, top)),
    maxWidth,
    maxHeight,
  }
}

function visibleViewport(): LauncherMenuViewport {
  const viewport = window.visualViewport
  return viewport
    ? { left: viewport.offsetLeft, top: viewport.offsetTop, width: viewport.width, height: viewport.height }
    : { left: 0, top: 0, width: window.innerWidth, height: window.innerHeight }
}

// ── LaunchButton ──
//
// Transforms into an inline menu on click:
//
//   Idle:      [+]
//   Open:      target context line + adapter list
//   Launching: [spinner]
//
// Double-click works because the default adapter occupies the exact same
// position as the + button. First click opens, second click hits default.
//
// Two modes:
//  - Explicit target: `cwd` and optional `peer` passed directly (used by
//    project hub folder rows where the target is known from topology).
//  - Context-aware: `sessions`, `selectedId`, and `fallbackCwd` passed;
//    the target is derived from the user's current context (used by
//    sidebar folder headers).

interface LaunchButtonProps {
  className?: string
  onLaunch?: () => void
  /** Async action to run before the launch request (e.g. seed a project). */
  beforeLaunch?: () => Promise<void>
  /** Explicit target: working directory for the new session. */
  cwd?: string
  /** Explicit target: peer name for remote launch. */
  peer?: string
  /**
   * Context-aware target: when `sessions` is provided, the launch target
   * is derived from the user's current context (selected session or most
   * recent alive session) instead of `cwd`/`peer`.
   */
  sessions?: Session[]
  selectedId?: string | null
  fallbackCwd?: string
}

export function LaunchButton({ className, onLaunch, beforeLaunch, cwd, peer, sessions, selectedId, fallbackCwd }: LaunchButtonProps) {
  // Resolve the target: context-aware mode (sessions provided) derives
  // a smart cwd, while an explicit `peer` prop is always authoritative
  // (the caller knows the folder's owner; ADR 0002). This matters for
  // peer folders whose sessions are all dead-resumable, where context
  // mode would otherwise lose the peer in resolveTarget's fallback.
  const target = useMemo((): LaunchTarget => {
    if (sessions && fallbackCwd !== undefined) {
      const ctx = resolveTarget(sessions, selectedId ?? null, fallbackCwd)
      return peer ? { ...ctx, peer } : ctx
    }
    return { peer, cwd: cwd || '' }
  }, [sessions, selectedId, fallbackCwd, cwd, peer])

  const showTarget = target.cwd !== ''

  const [state, setState] = useState<'idle' | 'open' | 'launching'>('idle')
  const [menuPos, setMenuPos] = useState<LauncherMenuPosition | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  // Read launcher config from the store (populated by /v1/health).
  const hasLaunchers = launchersSignal.value.length > 0
  const isOpen = state === 'open' && hasLaunchers

  // The target line and divider sit above the default launcher item.
  const targetOffset = showTarget ? 32 : 0

  /** Seed a fixed position; useLayoutEffect corrects it after measuring the portal. */
  const computeMenuPos = () => {
    const btn = btnRef.current
    if (!btn) return
    setMenuPos(launcherMenuPosition(
      btn.getBoundingClientRect(),
      { width: 180, height: 0 },
      visibleViewport(),
      targetOffset,
    ))
  }

  const handleClick = (e: MouseEvent) => {
    e.stopPropagation()
    if (state === 'idle') {
      computeMenuPos()
      setState('open')
    } else if (state === 'open') {
      setState('idle')
    }
  }

  const handleLaunch = async (id: string) => {
    setState('launching')
    if (beforeLaunch) await beforeLaunch()
    onLaunch?.()
    launchSession(id, { cwd: target.cwd || undefined, peer: target.peer }).finally(() => {
      setTimeout(() => setState('idle'), 600)
    })
  }

  useLayoutEffect(() => {
    if (!isOpen) return

    const reposition = () => {
      const btn = btnRef.current
      const menu = menuRef.current
      if (!btn || !menu) return
      const rect = menu.getBoundingClientRect()
      const next = launcherMenuPosition(
        btn.getBoundingClientRect(),
        { width: Math.max(rect.width, menu.scrollWidth), height: Math.max(rect.height, menu.scrollHeight) },
        visibleViewport(),
        targetOffset,
      )
      setMenuPos(current => current
        && current.left === next.left
        && current.top === next.top
        && current.maxWidth === next.maxWidth
        && current.maxHeight === next.maxHeight
        ? current
        : next)
    }

    reposition()
    const viewport = window.visualViewport
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    viewport?.addEventListener('resize', reposition)
    viewport?.addEventListener('scroll', reposition)
    return () => {
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
      viewport?.removeEventListener('resize', reposition)
      viewport?.removeEventListener('scroll', reposition)
    }
  }, [isOpen, targetOffset, target.peer, launchersSignal.value.length])

  // Close on outside press. The menu is portaled to <body> so it can
  // escape transformed/clipped ancestors such as the mobile sidebar; treat
  // both the original button container and the portaled menu as "inside".
  useEffect(() => {
    if (state !== 'open') return
    const handler = (e: Event) => {
      const targetNode = e.target as Node
      const inContainer = !!containerRef.current?.contains(targetNode)
      const inMenu = !!menuRef.current?.contains(targetNode)
      if (!inContainer && !inMenu) setState('idle')
    }
    const timer = setTimeout(() => document.addEventListener('pointerdown', handler), 0)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('pointerdown', handler)
    }
  }, [state])

  // Close on Escape
  useEffect(() => {
    if (state !== 'open') return
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') setState('idle') }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [state])

  const isLoading = state === 'launching'

  let defLauncher: LauncherDef | undefined
  let others: LauncherDef[] = []
  if (isOpen) {
    const resolved = launchersForPeer(
      launchersSignal.value, defaultLauncherSignal.value,
      peersSignal.value, target.peer,
    )
    defLauncher = resolved.launchers.find(l => l.id === resolved.default_launcher)
    others = resolved.launchers.filter(l => l.id !== resolved.default_launcher)
  }

  const menu = isOpen && menuPos ? (
    <div
      ref={menuRef}
      class="launch-inline-menu"
      style={{ top: menuPos.top, left: menuPos.left, maxWidth: menuPos.maxWidth, maxHeight: menuPos.maxHeight }}
    >
      {showTarget && (
        <>
          <div class="launch-target-line">{formatTarget(target)}</div>
          <div class="launch-inline-divider" />
        </>
      )}
      {defLauncher && (
        <button
          class="launch-inline-item launch-inline-default"
          onClick={(e) => { e.stopPropagation(); handleLaunch(defLauncher!.id) }}
        >
          {defLauncher.label}
        </button>
      )}
      {others.map((l, i) => (
        <button
          key={l.id}
          class="launch-inline-item"
          style={{ animationDelay: `${(i + 1) * 50}ms` }}
          onClick={(e) => { e.stopPropagation(); handleLaunch(l.id) }}
        >
          {l.label}
        </button>
      ))}
    </div>
  ) : null

  return (
    <div class={`launch-container ${className ?? ''}`} ref={containerRef}>
      <button
        ref={btnRef}
        class={`launch-btn ${isLoading ? 'loading' : ''}`}
        title={target.cwd
          ? target.peer ? `New session on ${target.peer} in ${target.cwd}` : `New session in ${target.cwd}`
          : 'New session'}
        onClick={handleClick}
      >
        {isLoading ? (
          <svg viewBox="0 0 16 16" width="14" height="14" class="spin">
            <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor"
              stroke-width="2" stroke-dasharray="28" stroke-dashoffset="8" stroke-linecap="round" />
          </svg>
        ) : '+'}
      </button>
      {menu && typeof document !== 'undefined' && createPortal(menu, document.body)}
    </div>
  )
}
