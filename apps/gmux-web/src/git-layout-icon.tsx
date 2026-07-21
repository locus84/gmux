import type { Session } from './types'

interface GitLayoutIconProps {
  layout: Session['git_layout']
}

/** Small, non-interactive marker for repository layout metadata. */
export function GitLayoutIcon({ layout }: GitLayoutIconProps) {
  if (layout === 'repository') {
    return (
      <svg
        class="session-git-layout-icon"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.4"
        stroke-linecap="round"
        stroke-linejoin="round"
        role="img"
        aria-label="Git repository"
        focusable="false"
      >
        <title>Git repository</title>
        <circle cx="4" cy="3" r="1.5" />
        <circle cx="4" cy="13" r="1.5" />
        <circle cx="12" cy="5" r="1.5" />
        <path d="M4 4.5v7M4 7h3a5 5 0 0 0 5-5v1.5" />
      </svg>
    )
  }

  if (layout === 'worktree') {
    return (
      <svg
        class="session-git-layout-icon worktree"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.35"
        stroke-linecap="round"
        stroke-linejoin="round"
        role="img"
        aria-label="Git worktree"
        focusable="false"
      >
        <title>Git worktree</title>
        <path d="M8 2v3M3.5 7V5h9v2" />
        <rect x="1.5" y="7" width="4" height="5" rx="1" />
        <rect x="6" y="7" width="4" height="5" rx="1" />
        <rect x="10.5" y="7" width="4" height="5" rx="1" />
      </svg>
    )
  }

  return null
}
