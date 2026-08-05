---
name: gmux
description: Drive long-running terminal commands and AI coding agents through gmux sessions. Use when the user asks to run a command in the background, send input to a running session, wait for an agent's turn to finish, orchestrate multiple agents in parallel, or capture output from a tmux/screen-style session.
---

# gmux

A command run through gmux becomes a managed session the user can watch live
in a browser. The grammar is verb-first; **running a command always uses the
explicit `--` separator** so gmux never guesses where its own flags end and
the command begins.

## Primitives

```bash
gmux -- <cmd> [args]         # run blocking; exits with the child's exit code
gmux -d -- <cmd> [args]      # run detached; prints the session id on stdout
gmux send <id> 'text' Enter  # submit; Pi uses its injected extension, not PTY typing
gmux send <id> C-c           # send a raw control key (interrupt)
gmux wait <id>               # block until the agent truly settles
gmux wait <id> --json        # for Pi, return the latest semantic result
gmux tail <id> [-n N]        # last N lines of output (ANSI stripped; default 100)
gmux ls [--json]             # list sessions (--json for machine parsing)
gmux kill <id>               # SIGTERM the runner but retain its session record
gmux session dismiss <id>    # terminate if needed and permanently remove the record
```

`ls` IDs are 8-character prefixes; pass them directly to
`send` / `wait` / `tail` / `kill`. Tip: `alias gm='gmux --'` makes
`gm pytest` shorthand for `gmux -- pytest`.

Because `gmux -- <cmd>` propagates the child's exit code, it composes:
`if gmux -- pytest -q; then ...`.

## Sending input and keys

`send` accepts literal text and trailing key names. **Enter is not implicit**.
For Pi sessions, exactly `text + Enter` (including piped text + Enter) is routed
through gmux's injected Pi extension via `sendUserMessage`; it does not touch the
PTY. Draft text without Enter, control/navigation keys, `send-keys`, and non-Pi
sessions remain raw terminal input:

```bash
gmux send $id 'pytest -q' Enter   # type and run
gmux send $id 'half a line'       # type without submitting
gmux send $id C-c                 # interrupt (Ctrl-C)
gmux send $id Escape              # send Escape
echo "$body" | gmux send $id Enter  # pipe stdin, then submit
req="review-$(date +%s)-$$"
gmux send --request-id "$req" --json $id 'review this' Enter
gmux wait $id --request-id "$req" --json
```

Key names follow tmux: `Enter`, `Tab`, `Escape`, `Up`/`Down`/`Left`/`Right`,
`C-c`, `C-d`, etc. For verbatim tmux compatibility there is also
`gmux send-keys -t <id> <keys...>` (with `-l` for literal text).

## Sequential orchestration

```bash
id=$(gmux -d -- pi "implement the feature")
gmux wait $id

gmux send $id "$(cat review.txt)" Enter
result=$(gmux wait $id --json)
printf '%s\n' "$result"
# Independently verify the worktree before cleanup.
gmux session dismiss $id
```

## Parallel orchestration

For isolated branches and working directories, load the separate
`gmux-worktree` skill and use `gmux worktree create` rather than manually
combining Git worktree and session setup.

```bash
ids=()
for ticket in fa-48 fa-49 fa-52; do
  ids+=( "$(gmux -d -- pi "Implement $ticket. Return when done.")" )
done

for id in "${ids[@]}"; do
  gmux wait "$id" --timeout 600 --json >"result-$id.json" || echo "$id failed: $?"
done

for id in "${ids[@]}"; do
  # Verify each worktree independently before dismissing its owned session.
  gmux session dismiss "$id"
done
```

## Waiting

`gmux wait <id>` blocks until an **agent** session goes idle or exits. For Pi it
uses the extension's correlated semantic request and `agent_settled`, so retries,
auto-compaction, and queued steering cannot create a false intermediate idle.
`--json` returns the latest semantic Pi request's bounded final response. With
multiple callers sharing one Pi session, provide the same stable
`--request-id ID` to both `send` and `wait`; the default wait intentionally
follows the session's latest semantic request. Exit codes:

- `0` agent reached idle (or session exited)
- `2` session died before going idle
- `3` `--timeout` elapsed
- `4` correlated Pi delivery/turn failed

Plain **shell** commands don't emit an idle signal and are rejected, so run
those blocking instead: `gmux -- make build < /dev/null` exits with the
command's own status. Never implement agent orchestration with repeated
`tail`/`sleep`/resend loops: send once, wait once, consume the structured result,
and treat `in_doubt` as requiring inspection rather than automatic retry.

## Worker cleanup

Hermes and other one-shot orchestrators own the IDs they launch. After an owned
worker reaches idle or exits, harvest its final output and repository/test evidence,
then run `gmux session dismiss <id>`. Dismiss only tracked worker IDs; never sweep
all sessions for a project. `dismiss` removes the session record but does not remove
its Git worktree. Keep user-opened interactive sessions resumable unless the user
asks to dismiss them.

## Other agents have one-shot modes

Agents stay running by default. To make them exit after one prompt, use the
agent's print mode: `pi -p`, `claude -p`, `codex exec`:

```bash
gmux -- pi -p "summarize this PR"
```

## Sessions on other machines

Sessions are **local by default** — bare IDs only ever match this host, so you
can't accidentally act on another machine. To address a peer session
explicitly, suffix the ID with `@<peer>` (see them with `gmux ls --all`):

```bash
gmux tail abc123@laptop
```

## Reference

- <https://gmux.app/reference/cli/>
- <https://gmux.app/integrations/scripts-and-agents/>
