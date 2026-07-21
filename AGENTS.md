# AGENTS.md

## Project context

`gmux` is a personal, web-first session workspace for watching and steering AI agents, test runners, and long-running commands across machines. The user actively uses this repository as their main workspace, so optimize for practical daily flow over demo polish.

Current product focus:

- **Mobile-first steering.** The desktop UI can be rich, but phone usage must be treated as a first-class workflow. The user often checks sessions and sends short steering messages from mobile; avoid interactions that require hover, precise drag, desktop keyboard shortcuts, or wide layouts.
- **File browser / viewer.** A near-term goal is to add a project/session-scoped file browser and lightweight file viewer so the user can inspect repository files from the same mobile workspace. Treat this as an auxiliary view next to the terminal, not as a replacement for the terminal.

## Code map

- `apps/gmux-web/` — Preact browser UI, xterm.js terminal, sidebar, project hub, mobile toolbar, settings.
  - `src/main.tsx` wires layout, routing, mobile terminal bar, viewport/keyboard tracking.
  - `src/store.ts` owns frontend signals. Raw state mirrors daemon snapshots; most UI state should be derived with `computed`.
  - `src/sidebar.tsx`, `src/session-row.tsx`, `src/terminal.tsx`, `src/styles.css` are the main UI surfaces.
- `services/gmuxd/` — Go daemon, HTTP/SSE/WebSocket API, session/project store, peering, auth, static frontend serving.
- `cli/gmux/` — Go runner/CLI that owns each PTY session and exposes its per-session socket.
- `packages/protocol/` — TypeScript schemas shared by web code; update these when adding API payloads used by the frontend.
- `packages/adapter/` — agent/test-runner adapters and hook integrations.
- `docs/adr/` — architecture decisions. Read relevant ADRs before changing persistence, peering, session identity, or streaming semantics.

## Mobile UX rules

- Design touch flows first for phone portrait. Use desktop affordances only as enhancements.
- Keep tap targets comfortable, roughly 44px where possible, and respect `env(safe-area-inset-*)`.
- Do not rely on hover, native drag-and-drop, or modifier-key shortcuts for essential actions.
- Avoid horizontal overflow and avoid layouts that require terminal-width mental math on small screens.
- Be careful with the soft keyboard: this app already tracks `visualViewport`, `keyboardOpen`, terminal focus, and mobile input/autocorrect behavior. Do not add focus-stealing controls that unexpectedly open or dismiss the keyboard.
- Preserve terminal continuity. Opening menus, file viewers, or sheets should not unnecessarily unmount xterm, lose scrollback, or reset selection/focus.
- Prefer bottom sheets, drawers, and routeable views that can be dismissed with Back/Escape and that keep the current session recoverable.
- Add tests for pure mobile/input helpers. For layout-only work, do a manual pass at phone widths and coarse pointer mode.

## File browser / viewer direction

When implementing file browsing:

- Scope browsing to a session/project root (`workspace_root` or `cwd`) by default. Do not expose arbitrary filesystem browsing unless the user explicitly asks for that product change.
- Enforce path safety in `gmuxd`, not only in the browser: clean paths, reject traversal, define how symlinks are handled, cap response sizes, and return typed errors.
- Keep the API peer-aware. A file view for a remote/devcontainer session should route to the owning daemon rather than pretending local paths are valid.
- Treat file contents as fetched data, not durable app state. Cache lightly if needed, but avoid adding persisted state for directory listings or file bodies.
- Start read-only. Editing files from the browser has higher security and conflict risk and should be a separate, explicit design.
- Handle common cases well: text preview, binary/too-large fallback, copy path/content, open/download, and useful empty/error states on mobile.

## Correctness

Prioritize verifiable, correct behavior above all else. If something has lots of race conditions or other edgecases that are difficult to test and reason about, it is likely the wrong approach. If a library does something more reliably than what we can achieve, it is worth considering.

## State discipline

Never add new state without justification. Before adding a field, ask: who owns it, who updates it, and can it be derived from existing state instead? Prefer derivation over storage. New state creates maintenance burden, sync bugs, and lifecycle complexity.

## Peering model

Hub-and-spoke: each node's SSE stream only includes sessions it **owns** (local + devcontainer). Network peer sessions are excluded. `PeerConfig.Local` distinguishes the two: only the Docker watcher sets it. Tailscale-discovered and manual peers are not Local.

## Commits and releases

Every commit on `main` is changelog material: the release pipeline
(`version.sh` + [git-cliff](https://git-cliff.org/)) reads commit
messages directly. Rules that follow from this:

- **Every commit is a conventional commit**, not just PR titles. Use
  `feat:`, `fix:`, `docs:`, `perf:`, `security:`, `refactor:`,
  `chore:`, `ci:`, `test:`, `style:`, `build:`. `feat!:` or
  `BREAKING CHANGE:` footer marks a major bump.
- **Scopes are optional but encouraged** for monorepo areas: `web`,
  `daemon`, `cli`, `adapter`, `peering`, `devcontainer`, `docs`.
  Example: `feat(peering): reconnect after system sleep`. Scopes show
  up as bold tags in the changelog: `- **(peering)** reconnect after
  system sleep`. The `release` scope is reserved for release-flow
  plumbing (workflows, notify-discord, cliff.toml itself) and is
  hidden from the changelog and bump computation: those changes
  affect maintainers, not users. Breaking release-scope changes
  (`feat(release)!:`) still surface so consumers of the release
  pipeline know to act.
- **Write commit messages as user-facing changelog bullets.** The text
  after `type: ` becomes the bullet verbatim. Lowercase, no trailing
  period, imperative mood. Good: `fix: prevent recursive config fetch
  storm`. Bad: `fix: Fixed the config storm issue.`.
- **Release behavior by type**: `feat` bumps minor, `fix` / `perf` /
  `security` bumps patch, `feat!` / `fix!` / `BREAKING CHANGE:` bumps
  major. `docs` appears in the changelog but doesn't trigger a
  release on its own. Everything else is hidden.
- **Security fixes** use `security:` (or `security!:` for breaking
  security changes) and appear in their own `### Security` section at
  the top of the release, right after Breaking.
- **PRs use rebase merge**, not squash. Atomic commits on feature
  branches land on `main` as-is, so keep them clean before pushing. Use
  `jj squash` / `jj split` / `jj describe` to fix up WIP commits
  locally.
- **Prose highlights for a release** live in the open `release/next`
  PR body, between the `<!-- prose-start -->` and `<!-- prose-end -->`
  markers. The PR body is the single source of truth: edit it
  directly in the GitHub UI, and the regen workflow re-syncs
  `changelog.mdx` and the bullets section of the body on every edit
  (and on every push to `main`). There is no `RELEASE_HIGHLIGHTS.md`
  file and no script to run after editing. Leave the prose section
  empty for patch-only releases that don't need curated prose; the
  Discord announcement falls back to the auto-generated bullet list
  so subscribers can still see what changed without clicking through.

## Worktree agent workflows

- `skills/gmux-worktree/SKILL.md` is the canonical skill for creating persistent, browser-visible gmux agent sessions in sibling Git worktrees. Keep its source in this repository; global and Hermes installations should symlink to that directory rather than copying it. Agents must inspect `gmux worktree ps --json` first, reuse a matching checkout/session, and create at most one worktree per logical task.
- The locus `pi-harness` `worktree-agents` extension is complementary, not a duplicate: use `start_worktree_agent` for one-shot child Pi implementation runs and `gmux worktree create` when a human needs to watch or steer a durable session from the web/mobile UI.
- Do not move or duplicate `gmux-worktree` into `locus-skills`; distinct ownership and trigger descriptions prevent drift and accidental tool substitution.

## Other rules

- Push changes and create pull requests. Don't commit directly to
  `main`.
- Use `./scripts/install.sh` only when the user explicitly approves a **full local install and gmuxd restart**. The script has no help/dry-run mode: even `./scripts/install.sh --help` performs the full build, replaces both `gmux` and `gmuxd`, and restarts the daemon. Never invoke it to inspect usage or for a web-only request.
- For a web-only install, use `./scripts/install-web.sh`. It builds the frontend and atomically replaces the configured external web directory without replacing or restarting gmuxd. The daemon must already have `web_dir = "~/.local/state/gmux/web"` (or the chosen `--dir`) in `~/.config/gmux/host.toml`; verify that configuration first. `install-web.sh --help` is safe and does not install.
