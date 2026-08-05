# ADR 0015: Semantic Pi messages over runner hooks

**Status:** Accepted
**Date:** 2026-08-05
**Related:** ADR 0011, `docs/runner-hook-protocol.md`

## Context

`gmux send <pi-session> <text> Enter` historically wrote text and carriage
return bytes to the PTY. HTTP success proved only that the runner attempted the
write. Pi might not have started, Enter could be lost or interpreted in a
transient TUI state, and a following `gmux wait` could observe the previous idle
snapshot before Pi reported `Working:true`.

Orchestrators compensated with `tail`/`sleep`/grep/resend loops. Those loops
cannot correlate output with input and can inject duplicate instructions into a
worker that is already progressing.

Pi exposes an official extension API for semantic user messages and exact
lifecycle hooks. gmux already injects an extension and exports the runner's
owner-only Unix socket to it.

## Decision

For Pi sessions, the submitted form `gmux send <id> <text> Enter` uses a
runtime-scoped semantic message relay between the runner and injected extension.
The extension calls `pi.sendUserMessage`; text without Enter, special/control
keys, `send-keys`, and other adapters remain raw PTY input.

The runner owns a bounded in-memory FIFO and status cache. Every request is
fenced by caller request ID, runner epoch, Pi runtime epoch, and sequence. An
idle send returns only after `before_agent_start`; a send during an active run is
queued as Pi steering. `gmux wait` prefers the correlated runner record and Pi
reports terminal state from `agent_settled`, not intermediate `agent_end`.
`gmux wait --json` exposes the bounded final assistant text and normalized
outcome.

A runtime switch or runner death marks unresolved delivery `replaced` or
`in_doubt`. gmux never retries such work automatically. Exactly-once execution
cannot be guaranteed when Pi accepts a message and dies before acknowledging it.

The relay is not durable task state. It has no DAG, dependencies, scheduler,
automatic retry policy, or history. Hermes remains responsible for durable task
records, independent verification, report storage, owned-session dismissal, and
worktree retention.

## Consequences

- Pi orchestration no longer depends on terminal paste timing or Enter delivery.
- `send` followed by `wait` cannot complete against the pre-send idle snapshot.
- Retry/compaction/steering continuations do not create false idle windows.
- Existing raw terminal controls and non-Pi integrations remain compatible.
- Existing live runners must be restarted to load the new embedded extension;
  unsupported runners fail explicitly instead of silently falling back to PTY
  submission.
- Results are bounded and disappear with the runner. A durable orchestrator must
  record them after retrieval.
