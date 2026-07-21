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
5. does not send a same-size resize solely to force inline-image redraw after a
   WebSocket reconnect.

Two narrow exceptions remain:

- The beta.288 core patch for iPad/WKWebView Korean IME behavior is retained.
  Input correctness was an explicit requirement and is independent of image
  rendering.
- The stale-WebSocket close guard is retained. It prevents an obsolete socket
  from marking its replacement disconnected and is connection-generation
  correctness, not a rendering workaround.

The runner-side hidden reconnect shrink remains in the installed CLI for now.
It is outside the web-only baseline deployment and can only be removed and
validated through a separately approved full CLI/daemon install.

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
- Reconnected or additional viewers may still be unable to reconstruct inline
  image protocol state from replayed text. If that is reproduced, fix the
  stream/replay boundary rather than automatically restoring a resize trick.
- A target-device smoke test after deployment showed the upgraded inline image
  rendering correctly, so no downstream rendering workaround was reapplied.

## Implementation record

- `cbd6ddc` — upgrade to the polished beta.288 HiDPI xterm build while retaining
  the Korean IME patch.
- `a581c5c` — interim attempt to adapt the old downstream safeguards; superseded
  by this decision.
- `d7a4749` — remove the downstream web rendering policies and deploy the
  upstream-default baseline.
