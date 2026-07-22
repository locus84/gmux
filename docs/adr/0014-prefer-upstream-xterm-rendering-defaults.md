# ADR 0014: prefer upstream xterm rendering defaults before downstream workarounds

**Status:** Accepted
**Date:** 2026-07-21
**Related:** ADR 0004 (session stream and replay), `apps/gmux-web/src/terminal.tsx`

## Context

gmux accumulated several browser-side workarounds while diagnosing mobile
inline-image, WebGL, and reconnect rendering problems:

- touch-specific image decode and storage limits;
- disabling WebGL on touch-capable devices;
- mobile Safari image-layer stacking overrides;
- a custom WebGL context-loss dispose/recreate circuit breaker; and
- a same-size resize reassertion on reconnect to provoke image redraws.

The xterm dependencies were then upgraded to the beta.288 generation, including
the polished HiDPI inline-image implementation and the WebGL texture-filtering
fix. Keeping all older workarounds enabled made it impossible to tell whether
the upgraded implementation already solved the original problems. Some of the
workarounds also conflicted with the new behavior: the CSS collapsed xterm's
top and bottom image layers, and touch detection disabled WebGL on hybrid
laptops as well as phones.

In this ADR, **upstream defaults** means the runtime behavior exposed by the
selected beta.288-gmux.5 xterm build: construct `ImageAddon` without gmux
options and construct `WebglAddon` without gmux lifecycle policy. The dependency
remains the gmux xterm fork because the selected polished HiDPI work and the
required input fixes are not all available in the published beta.288 packages.

## Decision

Use the upgraded xterm behavior as the baseline and add downstream workarounds
only after a regression is reproduced against that baseline.

The web client therefore:

1. loads `new ImageAddon()` with xterm's default limits and layer behavior on
   desktop and mobile;
2. attempts `new WebglAddon()` on every device, falling back to xterm's DOM
   renderer only when construction or activation fails;
3. does not override `.xterm-image-layer-top` or
   `.xterm-image-layer-bottom` stacking on mobile;
4. does not recreate WebGL addons after xterm reports context loss; and
5. does not send a same-size resize solely as a browser-side inline-image
   redraw trick after a WebSocket reconnect.

Three narrow exceptions remain:

- The beta.288 core patch for iPad/WKWebView Korean IME behavior is retained.
  Input correctness was an explicit requirement and is independent of image
  rendering.
- The stale-WebSocket close guard is retained. It prevents an obsolete socket
  from marking its replacement disconnected and is connection-generation
  correctness, not a rendering workaround.
- An automatic reconnect fetches and reasserts the daemon's current logical
  PTY size exactly once. The runner deliberately hides a one-column shrink
  while no client is attached, without changing session metadata, and requires
  the next explicit resize to restore that logical size and trigger `SIGWINCH`.
  Skipping the matching resize caused a reproduced mobile foreground
  regression: a snapshot rendered at `cols-1` was parsed by an xterm grid still
  at `cols`, displacing and overlaying TUI regions. The client uses a fresh,
  uncached session read rather than its viewport or suspended SSE/component
  cache, so it follows a size chosen by another device instead of reclaiming
  ownership.

The runner owns durable image recovery. Alongside its text-only `x/vt`
snapshot, it retains a bounded opaque replay checkpoint only after observing a
complete synchronized full redraw (`BSU`, display/home/scrollback reset, then
`ESU`). Subsequent bytes are retained in order only up to a strict cap and only
at complete terminal-sequence boundaries. On attach, the runner sends that raw
checkpoint and suffix so a fresh xterm receives the original Kitty/Sixel/IIP
payloads; if no safe checkpoint exists, it falls back to the existing ANSI
snapshot. gmuxd and peer proxies remain byte-transparent.

The runner-side hidden reconnect shrink remains as a compatibility/fallback
path. The web client must acknowledge it with the freshly resolved reconnect
resize above for existing runners and sessions that have no valid raw
checkpoint.

## Reapplication rule

Do not restore a workaround based only on the historical bug report. First:

1. reproduce the regression on the upstream-default baseline;
2. record browser/OS, device pixel ratio, image protocol, renderer, and
   reconnect state;
3. determine whether the defect belongs in xterm or gmux; and
4. apply the smallest scoped fix with a focused regression test.

In particular, avoid global canvas monkey patches, blanket touch-device WebGL
disabling, layer-wide `!important` overrides, and custom image viewers unless a
separate product decision explicitly requires them.

## Consequences

- gmux carries substantially less browser-specific rendering policy and tracks
  xterm's current image-layer and WebGL lifecycle semantics more closely.
- Mobile devices receive the same addon defaults as desktop, so real-device
  observation is required for memory pressure and Safari compositing failures.
- Reconnected and additional viewers recover inline images when the runner has
  a complete bounded raw checkpoint. Oversized, incomplete, or plain streams
  deterministically retain the ANSI snapshot fallback.
- The authoritative reconnect resize still completes the runner's existing
  hidden-shrink handshake for fallback and compatibility cases; image recovery
  itself belongs at the stream/replay boundary rather than in canvas or
  renderer tricks.
- A target-device smoke test after deployment showed the upgraded inline image
  rendering correctly, so no downstream rendering workaround was reapplied.

## Implementation record

- `cbd6ddc` — upgrade to the polished beta.288 HiDPI xterm build while retaining
  the Korean IME patch.
- `a581c5c` — interim attempt to adapt the old downstream safeguards; superseded
  by this decision.
- `d7a4749` — remove the downstream web rendering policies and deploy the
  upstream-default baseline.
- `cli/gmux/internal/ptyserver/replay_checkpoint.go` — retain bounded,
  protocol-safe raw redraw checkpoints for reconnect image recovery.
