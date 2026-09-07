// Launcher UI.
//
// Pure functions for launcher resolution + the LaunchButton component.
// Launcher definitions come from the store's health signal (populated
// from /v1/health). The component reads them reactively.

import { useState, useRef, useEffect, useLayoutEffect, useMemo, useId } from 'preact/hooks'
import { createPortal } from 'preact/compat'
import type { ComponentChildren } from 'preact'
import type { LauncherDef, PeerInfo } from './types'
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

/** Format a target for display: "~/dev/gmux" or "laptop: ~/dev/gmux". */
export function formatTarget(target: LaunchTarget): string {
  const shortCwd = target.cwd.replace(/^\/home\/[^/]+/, '~')
  if (target.peer) return `${target.peer}: ${shortCwd}`
  return shortCwd
}

// ── Menu positioning ──

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

/** Keep the measured menu inside the visual viewport, including keyboard offsets. */
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
// The launch target is explicit: callers pass `cwd` (the project's
// canonical dir) and optional `peer`. The button never derives a cwd
// from session context.

export interface LauncherMenuAction {
  label: string
  onSelect: () => void
  disabled?: boolean
}

interface LaunchButtonProps {
  className?: string
  onLaunch?: () => void
  menuActions?: LauncherMenuAction[]
  footerAction?: { label: string; onSelect: () => void }
  triggerContent?: ComponentChildren
  triggerLabel?: string
  showLaunchers?: boolean
  /** Async action to run before the launch request (e.g. seed a project). */
  beforeLaunch?: () => Promise<void>
  /** Working directory for the new session (the project's canonical dir). */
  cwd?: string
  /** Peer name for a remote launch; authoritative for the target's host. */
  peer?: string
}

export function LaunchButton({ className, onLaunch, menuActions = [], footerAction, triggerContent = '+', triggerLabel, showLaunchers = true, beforeLaunch, cwd, peer }: LaunchButtonProps) {
  const target = useMemo((): LaunchTarget => ({ peer, cwd: cwd || '' }), [peer, cwd])

  const showTarget = target.cwd !== ''

  const [state, setState] = useState<'idle' | 'open' | 'launching'>('idle')
  const [menuPos, setMenuPos] = useState<LauncherMenuPosition | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)
  const triggerId = useId()
  const menuId = useId()
  const menuRef = useRef<HTMLDivElement>(null)

  // Read launcher config from the store (populated by /v1/health).
  const hasLaunchers = showLaunchers && launchersSignal.value.length > 0

  const targetOffset = showTarget ? 32 : 0

  /** Seed a fixed position; the layout effect corrects it after measuring. */
  const positionMenu = () => {
    const btn = btnRef.current
    if (!btn) return
    setMenuPos(launcherMenuPosition(
      btn.getBoundingClientRect(),
      { width: 180, height: 0 },
      visibleViewport(),
      targetOffset,
    ))
  }

  useLayoutEffect(() => {
    if (state !== 'open') return

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
  }, [state, targetOffset, target.peer, launchersSignal.value.length])

  const handleClick = (e: MouseEvent) => {
    e.stopPropagation()
    if (state === 'idle') {
      positionMenu()
      setState('open')
    } else if (state === 'open') {
      setState('idle')
    }
  }

  const restoreTriggerFocus = () => queueMicrotask(() => btnRef.current?.focus())

  const handleLaunch = async (id: string) => {
    setState('launching')
    restoreTriggerFocus()
    if (beforeLaunch) await beforeLaunch()
    onLaunch?.()
    launchSession(id, { cwd: target.cwd || undefined, peer: target.peer }).finally(() => {
      setTimeout(() => setState('idle'), 600)
    })
  }

  const handleMenuKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Tab') {
      e.preventDefault()
      const selector = 'a[href],button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex]:not([tabindex="-1"])'
      const menu = menuRef.current
      const tabbables = [...document.querySelectorAll<HTMLElement>(selector)].filter(element =>
        !menu?.contains(element) && element.getClientRects().length > 0,
      )
      const triggerIndex = tabbables.indexOf(btnRef.current!)
      const nextIndex = e.shiftKey ? triggerIndex - 1 : triggerIndex + 1
      const next = tabbables[nextIndex] ?? btnRef.current
      setState('idle')
      requestAnimationFrame(() => next?.focus())
      return
    }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return
    const items = [...(menuRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
    if (items.length === 0) return
    const current = items.indexOf(document.activeElement as HTMLButtonElement)
    let next = 0
    if (e.key === 'End') next = items.length - 1
    else if (e.key === 'ArrowUp') next = current <= 0 ? items.length - 1 : current - 1
    else if (e.key === 'ArrowDown') next = current < 0 || current === items.length - 1 ? 0 : current + 1
    e.preventDefault()
    items.forEach((item, index) => { item.tabIndex = index === next ? 0 : -1 })
    items[next].focus()
  }

  // Close on outside press. pointerdown rather than mousedown, for the
  // same reason as the other two popovers: touch gestures that
  // preventDefault kill the synthesized mouse cascade, and an outside
  // press that never arrives leaves the menu stranded.
  useEffect(() => {
    if (state !== 'open') return
    const handler = (e: PointerEvent) => {
      const t = e.target as Node
      if (containerRef.current?.contains(t)) return
      if (menuRef.current?.contains(t)) return
      setState('idle')
    }
    const timer = setTimeout(() => document.addEventListener('pointerdown', handler), 0)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('pointerdown', handler)
    }
  }, [state])

  // Move keyboard focus into the portaled menu once it is positioned.
  useEffect(() => {
    if (state !== 'open' || !menuPos) return
    const frame = requestAnimationFrame(() => menuRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus())
    return () => cancelAnimationFrame(frame)
  }, [state, menuPos])

  // Close on Escape and return focus to the trigger.
  useEffect(() => {
    if (state !== 'open') return
    const handler = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      setState('idle')
      btnRef.current?.focus()
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [state])

  const isOpen = state === 'open' && (hasLaunchers || menuActions.length > 0 || !!footerAction)
  const isLoading = state === 'launching'

  let defLauncher: LauncherDef | undefined
  let others: LauncherDef[] = []
  if (isOpen && showLaunchers) {
    const resolved = launchersForPeer(
      launchersSignal.value, defaultLauncherSignal.value,
      peersSignal.value, target.peer,
    )
    defLauncher = resolved.launchers.find(l => l.id === resolved.default_launcher)
    others = resolved.launchers.filter(l => l.id !== resolved.default_launcher)
  }

  const launchLabel = target.cwd
    ? target.peer ? `New session on ${target.peer} in ${target.cwd}` : `New session in ${target.cwd}`
    : 'New session'
  const buttonLabel = triggerLabel ?? launchLabel
  const firstActionIndex = menuActions.findIndex(action => !action.disabled)
  const hasEnabledMenuAction = firstActionIndex >= 0

  return (
    <div class={`launch-container ${className ?? ''} ${isOpen ? 'open' : ''}`} ref={containerRef}>
      <button
        ref={btnRef}
        id={triggerId}
        class={`launch-btn ${isLoading ? 'loading' : ''}`}
        title={buttonLabel}
        aria-label={buttonLabel}
        aria-haspopup="menu"
        aria-expanded={isOpen}
        aria-controls={isOpen ? menuId : undefined}
        onClick={handleClick}
      >
        {isLoading ? (
          <svg viewBox="0 0 16 16" width="14" height="14" class="spin">
            <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor"
              stroke-width="2" stroke-dasharray="28" stroke-dashoffset="8" stroke-linecap="round" />
          </svg>
        ) : triggerContent}
      </button>
      {/* Portaled to <body>: on mobile the sidebar is a transformed drawer
          (translateX), which makes it the containing block for fixed
          descendants — rendered in place the menu would be positioned
          relative to the sidebar and clipped at its edge. The portal keeps
          the fixed coords viewport-relative on every layout. */}
      {isOpen && menuPos && createPortal(
        <div
          ref={menuRef}
          id={menuId}
          class="launch-inline-menu"
          role="menu"
          aria-labelledby={triggerId}
          onKeyDown={handleMenuKeyDown}
          style={{
            top: menuPos.top,
            left: menuPos.left,
            minWidth: Math.min(180, menuPos.maxWidth),
            maxWidth: menuPos.maxWidth,
            maxHeight: menuPos.maxHeight,
          }}
        >
          {showTarget && (
            <>
              <div class="launch-target-line">{formatTarget(target)}</div>
              <div class="launch-inline-divider" />
            </>
          )}
          {menuActions.map((action, index) => (
            <button
              key={action.label}
              class="launch-inline-item launch-inline-action"
              role="menuitem"
              tabIndex={index === firstActionIndex ? 0 : -1}
              disabled={action.disabled}
              onClick={(e) => { e.stopPropagation(); setState('idle'); restoreTriggerFocus(); action.onSelect() }}
            >
              {action.label}
            </button>
          ))}
          {(defLauncher || others.length > 0) && (
            <>
              {menuActions.length > 0 && <div class="launch-inline-divider" />}
              <div class="launch-inline-section-label">New session</div>
            </>
          )}
          {defLauncher && (
            <button
              class="launch-inline-item launch-inline-default"
              role="menuitem"
              tabIndex={!hasEnabledMenuAction ? 0 : -1}
              onClick={(e) => { e.stopPropagation(); handleLaunch(defLauncher!.id) }}
            >
              {defLauncher.label}
            </button>
          )}
          {others.map((l, index) => (
            <button
              key={l.id}
              class="launch-inline-item"
              role="menuitem"
              tabIndex={!hasEnabledMenuAction && !defLauncher && index === 0 ? 0 : -1}
              onClick={(e) => { e.stopPropagation(); handleLaunch(l.id) }}
            >
              {l.label}
            </button>
          ))}
          {footerAction && (
            <>
              {(menuActions.length > 0 || defLauncher || others.length > 0) && <div class="launch-inline-divider" />}
              <button
                class="launch-inline-item launch-inline-footer"
                role="menuitem"
                tabIndex={!hasEnabledMenuAction && !defLauncher && others.length === 0 ? 0 : -1}
                onClick={(e) => { e.stopPropagation(); setState('idle'); restoreTriggerFocus(); footerAction.onSelect() }}
              >
                {footerAction.label}
              </button>
            </>
          )}
        </div>,
        document.body,
      )}
    </div>
  )
}
