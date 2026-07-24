# Runner hook protocol: tool-neutral authoritative session events

**Status:** Stable · **Related:** ADR 0011, `cli/gmux/internal/ptyserver`

The contract an agent implements to report its session state to the gmux runner
authoritatively. Tool-neutral: the runner makes no per-adapter assumptions in
`handleHookEvent`. pi's extension (`agentext/pi-ext.mjs`) is the reference; the
protocol is not pi-specific.

Per ADR 0011, live state is runner-owned. An agent reports its own facts (held
file, turn phase) rather than the daemon inferring them from fs scans/scrollback
— and a hook even catches a cache-served `/resume` that reads no file. The
runner only **relays** these facts; the one bit of state it keeps is a snapshot
replayed to `/events` (so a restarted daemon re-learns attribution), never used
to guess.

## Transport

- Runner exports `GMUX_SESSION_SOCK` (its Unix socket) to the agent env.
- Agent POSTs JSON to `POST /hook/event`, **fire-and-forget**: a failed POST
  must never surface into the agent; the next event re-establishes truth.
- Socket is owner-only (0o700).

## Event schema

One JSON object per event, discriminated by `op`. Unknown ops/values are ignored
(forward-compatible). An empty explicit `title` name is meaningful and clears
the mux-owned name; other zero-value fields remain no-ops.

```jsonc
// op "session" — authoritative bind. Sent on startup and on every rebind
// (switch/new/resume/fork).
{
  "op":     "session",
  "path":   "/abs/path/to/conversation-file",  // may be empty until a new file exists
  "id":     "session-id",                       // optional; slugified for the URL if no slug
  "slug":   "human-title",                      // optional; explicit URL-safe slug, preferred over id
  "name":   "generated fallback",                // optional; sets the adapter title
  "agent_title": "user-chosen agent name",       // optional; empty clears it on rebind
  "cwd":    "/project/dir",                      // optional; accepted, not yet applied
  "reason": "startup|new|resume|fork|activity"  // optional; informational
}

// op "title" — explicit user-authored session name. Empty clears it.
{ "op": "title", "name": "human title" }

// op "turn" — agent loop boundary.
{ "op": "turn", "phase": "start" }                            // → working
{ "op": "turn", "phase": "end", "outcome": "completed",       // see vocabulary
  "title": "human title" }                                    // optional
```

### Field reference

| Field     | Op       | Meaning |
|-----------|----------|---------|
| `path`    | session  | Absolute path of the held conversation file; may be empty on a brand-new bind. |
| `id`      | session  | Session identity; slugified into the URL when no `slug`. |
| `slug`    | session  | Explicit URL-safe slug; preferred over `id` (e.g. codex's UUID slugifies badly). |
| `name`        | session | Adapter-generated fallback title at bind time. |
| `agent_title` | session | Agent-native explicit name, applied atomically with a bind; empty clears it. |
| `name`        | title   | Agent-native explicit name; an empty string clears it. |
| `cwd`     | session  | Project dir. Accepted for forward-compat but not applied — the runner knows the launch cwd. |
| `reason`  | session  | Why the bind happened; informational. |
| `phase`   | turn     | `"start"` or `"end"`. |
| `outcome` | turn end | Normalized terminal state — see below. |
| `title`   | turn end | Adapter-generated fallback title at turn end. |

The runner keeps these sources separate and resolves them as:
`gmux explicit title > agent-native explicit title > application OSC title > adapter fallback > command`.
An agent should only send `op: "title"` for a name explicitly chosen inside the
agent, not for an inferred title or decorated terminal title. A name set with
`gmux session rename` remains the higher-priority multiplexer override.

### Outcome vocabulary

Stable and agent-agnostic; each hook normalizes its native state into one. The
outcome→sidebar mapping is gmux policy in the runner (`applyTurnEnd`), not the
agent's concern.

| Outcome     | Meaning                          | Sidebar              |
|-------------|----------------------------------|----------------------|
| `completed` | Agent finished its own turn.     | idle + **unread**    |
| `aborted`   | User interrupted (Esc).          | idle                 |
| `error`     | Agent gave up.                   | idle + **error**     |

## The runner does NOT, for hooked sessions

Parse the conversation file, infer status from PTY/scrollback, apply per-adapter
heuristics in `handleHookEvent`, or use the `session_file` snapshot for anything
but `/events` replay.

## Implementing for a new agent

1. **Load the hook** via the seam matching how the agent loads extensions
   (below). Both are ephemeral, scoped to the launch, and no-op without
   `GMUX_SESSION_SOCK`.
2. Report a `session` event on every bind.
3. If the agent supports explicit user naming, report `title` immediately when
   it changes and clear it when switching to an unnamed session.
4. Report `turn` start/end, normalizing to the outcome vocabulary.

### Injection seams

- **`SessionExtender`** (pi): the runner materializes the embedded pi extension
  and splices `pi -e <path>` into the argv.
- **`SessionHookCommand`** (codex): the runner injects a `gmux __codex-hook`
  command hook via the agent's config-override flags (`-c hooks.<Event>=...`),
  with the gmux binary itself as the hook program. It also carries the per-hook
  `trusted_hash` codex computes so only gmux's own hooks are trusted (never the
  global `--dangerously-bypass-hook-trust`). Version-gated; older codex falls
  back to daemon metadata attribution, and a hash mismatch degrades to the same
  fallback rather than broadening trust.
- **`SessionHookCommand`** (claude): Claude Code takes hooks through settings,
  so the runner splices `--settings <inline-json>` (a `gmux __claude-hook`
  command hook). That layer merges with the user's settings and hook arrays
  concatenate, so gmux's hooks add to rather than clobber the user's.
