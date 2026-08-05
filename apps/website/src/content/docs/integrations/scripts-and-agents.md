---
title: Scripts and agents
description: Drive gmux sessions from shell scripts, CI, and agent harnesses.
sidebar:
  order: 0
---

`gmux` is designed so the same binary works whether you attach to a session by hand or drive sessions from a script. This page covers the scripted shape: starting sessions non-interactively, sending input, waiting for output, and composing these into agent-orchestration patterns.

:::tip[Driving gmux from an agent?]
Install the [gmux skill](https://github.com/gmuxapp/gmux/blob/main/skills/gmux/SKILL.md) so your agent picks up these patterns automatically:

```sh
npx skills add gmuxapp/gmux
```

The skill follows the [agentskills.io](https://agentskills.io/) standard and works with Claude Code, Codex, Cursor, Copilot, Gemini CLI, OpenCode, and 50+ other agents. Or drop the `SKILL.md` into your agent's skills directory by hand if you prefer not to install the CLI.
:::

## The piped flow

The most useful primitive for scripting is `gmux -- <cmd>` with stdin redirected away from a terminal:

```bash
gmux -- make build < /dev/null
gmux -- pi -p "summarize this PR" < /dev/null
```

Running a command always uses the explicit `--` separator — there is no bare `gmux <cmd>` shorthand. The `-p` (print) flag tells `pi` to process the prompt and exit instead of staying interactive; other agents have similar one-shot modes (`claude -p`, `codex exec`). Without one, the agent stays running and the call blocks indefinitely — for multi-turn work, spawn detached and drive it instead (see [parallel orchestration](#parallel-orchestration)).

When stdin is not a TTY, `gmux -- <cmd>`:

- **Blocks** until the child exits.
- **Streams bounded metadata** to stdout (session id, kind, exit status), not the full PTY output. Your script's logs stay readable.
- **Exits with the child's exit code**, so `gmux -- make build < /dev/null && deploy.sh` works.
- **Keeps the session in the UI** for the duration: a human can watch it live in the browser without affecting the script.

This is the shape every other line on this page builds on. It works the same in CI, cron jobs, and agent harnesses (whose stdin is a pipe by default).

## Spawning detached

To start a session and drive it later, spawn it detached with `-d`. It returns immediately and prints the session id:

```bash
id=$(gmux -d -- pi "build the feature")
```

Capture that id and pass it to `send`, `wait`, `tail`, and `kill`.

## Sending input

`gmux send <id> [text] [keys]` sends input to a running session. For Pi,
`text + Enter` uses the injected extension's semantic `sendUserMessage` path;
raw drafts/special keys and other agents keep terminal semantics.
**Submission is explicit** — add a trailing `Enter` to dispatch a line:

```bash
gmux send <id> 'shorter inline message' Enter
gmux send <id> Enter < prompt.txt          # pipe a file, then submit
gmux send <id> C-c                          # interrupt, no Enter
```

When no text is given and stdin is a pipe, gmux reads stdin until EOF (capped at 1 MiB). `send` is gated by Unix-socket file permissions (owner-only). If multiple orchestrators share one Pi session, pass one stable `--request-id` to both `send` and `wait`; see the [CLI reference](/reference/cli/).

## Waiting

`gmux wait <id>` blocks until an agent session finishes its current turn — the primitive that turns sequential orchestration into one line per step:

```bash
gmux send <id> Enter < step-1.txt
gmux wait <id>

gmux send <id> Enter < step-2.txt
result=$(gmux wait <id> --json) # correlated final Pi answer
printf '%s\n' "$result"
```

For Pi, `wait` targets the extension-correlated request and returns only at
`agent_settled`; it cannot race against the pre-send idle snapshot. Exit codes:
`0` settled/idle, `2` died/replaced/in-doubt, `3` timeout, `4` Pi delivery/turn
failed. Never orchestrate with `tail`/sleep/grep/resend loops.

`wait` is for agent sessions (`claude`, `codex`, `pi`); shell sessions have no working signal and are rejected with a clear error. To wait for a shell command, run it through the blocking piped flow above (`gmux -- make build < /dev/null`) — that's exactly the shape `gmux -- <cmd>` already provides. (Waiting on arbitrary output — "until this text appears" — is planned as a server-side `wait` condition, [#313](https://github.com/gmuxapp/gmux/issues/313).)

## Reading output

`gmux tail <id>` prints recent output as plain text (ANSI stripped; `-n N` for
line count, default 100). It is a human diagnostic snapshot, not an
orchestration signal. Retrieve Pi results with `gmux wait --json` and parse the
returned JSON rather than scraping terminal output.

## Discovery and cleanup

```bash
gmux ls            # all local sessions, alive first, newest first
gmux ls --json     # machine-readable, for parsing in scripts
gmux kill <id>     # SIGTERM the runner, normal exit lifecycle
```

Every verb accepts id prefixes, full session ids, or slugs, so the eight-character short form `ls` prints passes straight back to `kill`, `send`, `tail`, or `wait`.

## Parallel orchestration

Spawn N agents in parallel, then wait for each in turn. Sequential waiting finishes when the slowest agent does — same wall-clock as backgrounding the `wait` calls, but exit codes are per-session and the loop reads as a straight line:

```bash
ids=()
for ticket in fa-48 fa-49 fa-52; do
  ids+=( "$(gmux -d -- pi "Implement $ticket. Return when you're done.")" )
done

for id in "${ids[@]}"; do
  gmux wait "$id" --timeout 600 || echo "$id did not finish cleanly: $?"
done

for id in "${ids[@]}"; do
  echo "=== $id ==="
  gmux tail "$id" -n 100
done
```

The agents run concurrently because `gmux -d -- pi <prompt>` returns as soon as the session registers and prints just the session id (no grep needed); the wait loop gates the harvest step on every agent reaching idle.

## Isolated Git worktrees

When parallel agents must edit independently, create the checkout and gmux
session as one operation:

```bash
gmux worktree ps --json  # reuse a matching checkout/session when present

result=$(gmux worktree create fix-login \
  --base origin/main --agent pi \
  --prompt "Implement the login fix and run focused tests" --json)
id=$(printf '%s' "$result" | jq -r '.session_id')
path=$(printf '%s' "$result" | jq -r '.path')

gmux wait "$id" --timeout 900
gmux tail "$id" -n 200
git -C "$path" status --short
```

The default destination mirrors the repository's canonical absolute path under
`$XDG_DATA_HOME/gmux/worktrees` (normally `~/.local/share/gmux/worktrees`) and
flattens branch slashes in the final directory name. For example,
`/Users/me/src/app` plus `fix/login` becomes
`~/.local/share/gmux/worktrees/Users/me/src/app/fix-login`. Worktree operations
are local-only, reject branch/path collisions, and do not include uncommitted
source checkout changes in a worktree created from `HEAD`. Install or load the
[`gmux-worktree` skill](https://github.com/gmuxapp/gmux/blob/main/skills/gmux-worktree/SKILL.md)
for parallel tracking, recovery, review, and cleanup guidance.

## Nested gmux

When `gmux -- <cmd>` runs inside an existing gmux session (detected via the `GMUX=1` env var), gmux auto-detaches into a headless background process so you don't get a PTY-within-PTY. The auto-detach only triggers when stdin is a TTY: agent harnesses whose stdin is a pipe land in the piped flow above and behave normally. You don't need to special-case nested invocations.

## Agent-specific integrations

Each adapter has its own status and resumption story. See:

- [pi](/integrations/pi/)
- [Codex](/integrations/codex/)
- [Claude Code](/integrations/claude-code/)
