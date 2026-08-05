---
name: gmux-worktree
description: Create and inspect isolated Git worktrees that run coding agents through gmux. Use when parallel implementation or review tasks need separate branches and working directories, or when the user asks to launch a gmux agent in a worktree.
compatibility: Requires git, gmux, gmuxd, and an available gmux coding-agent launcher such as pi, Claude Code, or Codex.
---

# gmux worktree

When this skill is loaded, **MUST use `gmux worktree create`** for creation and
agent launch. Do not substitute raw `git worktree add` plus `cd` plus `gmux`; use
that only as an explicitly reported fallback when the installed gmux lacks the
worktree namespace. Never use the removed bare `gmux <command>` form—normal
session launches require `gmux -- <command>`.

Commands are local-only: never interpret a peer's path on this machine.

## Reuse before create

Before every create, **MUST run `gmux worktree ps --json`** in the target
repository and compare the requested task with existing branch, path, and live
sessions.

- Reuse an existing matching worktree/session instead of creating another one.
- Create at most one worktree per logical task. Substeps, retries, and follow-up
  prompts stay in the same session via `gmux send` and `gmux wait`.
- Never call `gmux worktree create` twice for one task unless the first command
  failed before creation and a fresh `worktree ps` confirms that no checkout was
  created.
- Multiple worktrees are allowed only for explicitly separate parallel tasks;
  keep a task → branch → path → session-id table and create one row per task.
- When launching Hermes through `--agent hermes`, do not also pass Hermes's own
  `--worktree` flag: gmux already created and selected the checkout.

## Inspect

```bash
gmux worktree current
gmux worktree current --json
gmux worktree ps
gmux worktree ps branch:fix/login --json
```

Selectors are `current`, `branch:<branch>`, `path:<path>`, and
`name:<worktree-directory>`. A unique bare branch, path, or directory name also
works. `ps` reports live local gmux sessions grouped by their actual checkout cwd.

## Create and launch

First inspect and reuse:

```bash
gmux worktree ps --json
```

Only when no matching checkout exists:

```bash
result=$(gmux worktree create fix-login \
  --base origin/main \
  --agent pi \
  --prompt "Implement the login fix, run focused tests, and report changed files." \
  --json)
id=$(printf '%s' "$result" | jq -r '.session_id')
path=$(printf '%s' "$result" | jq -r '.path')
```

Defaults:

- repository: the enclosing Git checkout
- base: `HEAD`
- destination: `$XDG_DATA_HOME/gmux/worktrees/<full-repository-path>/<name>`, defaulting to `~/.local/share/gmux/worktrees/...`; slashes in the branch become dashes in the final directory name
- no agent session unless `--agent` is supplied

Use `--repo <path>` and `--path <path>` when defaults are inappropriate. gmux
rejects existing branches and destination paths rather than silently reusing them.
A prompt requires an agent.

For Pi launchers, the initial prompt is delivered through gmux's injected Pi
extension and `worktree create` returns only after Pi starts the correlated turn;
it is never typed into the PTY. If first-run trust/login prevents the extension
from starting, gmux preserves the worktree and session and reports ambiguous
in-flight delivery. Resolve the dialog in the browser, then inspect the existing
request with `gmux wait --json`; do not blindly create or resend.

## Track and harvest

```bash
worker_result=$(gmux wait "$id" --timeout 900 --json)
printf '%s\n' "$worker_result"
git -C "$path" status --short
git -C "$path" diff --stat
# After recording the final report and verification evidence:
gmux session dismiss "$id"
```

For parallel work, keep an explicit task → session id → path → branch table. Create
all worktrees first, then wait and review each result. `gmux wait` is for agent
sessions, not arbitrary shell commands. One-shot orchestrators must dismiss only
the session IDs they created after harvesting output; dismissal leaves the worktree,
commits, and files intact. Keep user-opened interactive sessions unless the user asks
to dismiss them. Never poll `gmux tail`, sleep, grep for a verdict, or resend a
"continue" prompt: one semantic send plus one correlated wait is the lifecycle.

## Safety

- Uncommitted changes in the source checkout are not included when creating from
  `HEAD`; choose and verify the base ref deliberately.
- If launch or prompt delivery fails, gmux preserves the new checkout and reports
  its path. Accepted-but-unacknowledged Pi delivery is `in_doubt`; inspect the
  same session/request and never auto-resend or create a replacement worktree.
- Session failure or timeout never proves a worktree is disposable. Harvest any
  available output, dismiss the owned worker session, and preserve the worktree for
  recovery.
- gmux intentionally has no `worktree rm` command yet. Review status, commits, and
  integration before manually running `git worktree remove`; do not force removal
  by default.
- Worktrees isolate tracked files, not shared databases, ports, caches, credentials,
  or external services.
