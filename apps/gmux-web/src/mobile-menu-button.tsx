import { unreadCount } from './store'
import { IconRefresh } from './sidebar'
import { useArrivalPulse } from './use-arrival-pulse'

interface MobileMenuButtonProps {
  onOpen: () => void
  className?: string
}

/** Standalone mobile hamburger for non-terminal pages where the terminal
 * toolbar is not mounted. Hidden on desktop by CSS. */
export function MobileMenuButton({ onOpen, className = '' }: MobileMenuButtonProps) {
  // Match the terminal toolbar hamburger: surface only waiting/unread
  // background sessions and re-pulse when the count changes.
  const waitingCount = unreadCount.value
  const waiting = waitingCount > 0
  const arrival = useArrivalPulse(waiting ? 'unread' : 'none', waitingCount)
  const classes = [
    'mobile-menu-button',
    className,
    waiting ? 'bg-waiting' : '',
    arrival ? `bg-${arrival}` : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      type="button"
      class={classes}
      onClick={() => {
        // If a touch keyboard is open, opening the sidebar should make room
        // for the drawer rather than leaving the keyboard in front of it.
        (document.activeElement as HTMLElement | null)?.blur()
        onOpen()
      }}
      aria-label="Open sessions"
      title="Open sessions"
    >
      <span aria-hidden="true">☰</span>
    </button>
  )
}

export function MobileRefreshButton() {
  return (
    <button
      type="button"
      class="mobile-menu-button mobile-refresh-button"
      onClick={() => location.reload()}
      aria-label="Refresh app"
      title="Refresh app"
    >
      <IconRefresh />
    </button>
  )
}
