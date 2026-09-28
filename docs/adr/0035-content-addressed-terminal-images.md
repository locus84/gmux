# ADR 0035: content-addressed terminal images

**Status:** Transport retained; image-reference UI withdrawn
**Date:** 2026-09-15

## Current policy (2026-09-21)

Use existing viewer/file links only. The browser no longer creates image
placeholders, decorations, a tray, or a separate asset viewer. For Pi sessions
only, it requests `images=refs-v1` to avoid decoding legacy payloads, and ignores the
resulting OSC references. Runner caches and authenticated retrieval routes are
retained for protocol compatibility, not exposed as a new UI.

New gmux PTY children receive `PI_IMAGE_PROTOCOL=none`: Pi's automatic image
capability detection then avoids allocating image-height blank rows. Explicit
Pi `terminal.images` settings can override this environment policy. Other
applications retain their native graphics capability. Existing processes and
recorded terminal geometry are not rewritten; adopting the producer policy
requires a newly started process. Do not strip arbitrary blank terminal rows.

The original design below records the transport rationale; its placeholder,
tray, preview, and click-to-fetch UI policy is superseded by this section.

## Context

Browser terminal clients currently receive image bytes inside Kitty graphics
escape sequences. Those bytes are base64-expanded and travel on the
browser-facing WebSocket without compression. A reconnect sends an image-aware
raw checkpoint of up to 16 MiB plus a suffix of up to 2 MiB. A viewport claim
can then resize the PTY and make a TUI redraw the same images again.

This is acceptable on a LAN but performs badly over cellular. A measured attach
to an image-heavy `pixel-test` session transferred roughly 12.7 MiB. At an
effective 2 Mbit/s that payload alone takes about 50 seconds, before browser
base64 decoding, image decoding, and terminal parsing.

Re-enabling `permessage-deflate` is not an acceptable fix. It previously caused
a reconnect storm in an affected mobile browser (#279), and compressed image
formats gain little from generic transport compression. Merely hiding inline
images in the browser also does not help because the expensive bytes have
already crossed the network.

The file API is intentionally scoped to a session/project workspace. Expanding
it to arbitrary `/tmp` paths would couple terminal rendering to filesystem
access and violate that safety boundary. Peer-owned sessions also require the
image request to route to the owning daemon.

## Decision

Extract supported inline image payloads in the session runner and replace them,
for browser clients only, with a compact gmux image reference. Store the emitted
bytes in a bounded, content-addressed cache owned by that runner.

### Identity and storage

- The key is the lowercase SHA-256 digest of the decoded image bytes.
- The cache is in memory, byte-bounded, and evicts least-recently-used entries.
- It is not durable application state and is discarded with the runner.
- There is no list API and no path-based lookup.
- Repeated redraws of identical bytes reuse one entry.

The runner keeps the original PTY stream unchanged for its emulator,
scrollback, local terminal output, prompt parsing, and non-browser attaches.
Only browser replay/live publication receives compact references.

### Supported protocol

The first implementation targets Pi's direct Kitty PNG subset: `a=T,f=100`,
`C=1,q=2`, a positive image ID, and explicit positive `c`/`r`. It accepts
omitted `t` or `t=d`, single-chunk payloads, and `m=1`/`m=0` multipart payloads.
It also translates Pi's subsequent `a=p,C=1,q=2` placements from a bounded
ID-to-asset metadata cache, including validated paired `y`/`h` source crops.
Cropped placements use their `c`/`r` geometry, but opening the preview shows
the full original PNG rather than a cropped duplicate. Placement-only deletion (`d=a`) retains that map;
data deletion (`d=A`/`d=I`) invalidates it. Original delete commands still reach
the native image addon so raw images can coexist with references.

The browser explicitly requests `client=browser&images=refs-v1`. Runner output
uses synchronous `OSC 777;gmux-image;<JSON> ST` messages for put/delete events;
metadata includes version, SHA-256, image ID, cell dimensions, PNG byte count,
and media type. No image fetch or bitmap decoding occurs in the terminal parser.

Supported PNGs are limited to 8 MiB, 8192 pixels on either axis, and 16 million
pixels. The runner validates the PNG header and dimensions without decoding the
pixels. The browser revalidates these limits before native image decoding.
Unknown, malformed, oversized, or unsupported transmissions retain their raw
path. This is not a full Kitty protocol implementation; non-Pi modes still have
the original image transport/rendering limitations.

A multipart image is an attach boundary: replay cannot be snapshotted halfway
through one.

### HTTP retrieval

The owning runner serves one image by digest. `gmuxd` exposes a session-scoped
route and forwards peer sessions through the existing owner-aware GET path.
The route:

- validates a 64-character lowercase hexadecimal digest;
- verifies that the session exists and is owned by the selected runner;
- serves only bytes previously emitted by that runner;
- sets a sniffed image content type, `X-Content-Type-Options: nosniff`, and
  `Cache-Control: private, max-age=31536000, immutable`;
- returns a typed expired/not-found response after cache eviction.

The browser never receives or submits the source filesystem path. This also
handles agent-created files under `/tmp` without granting arbitrary `/tmp`
browsing.

### Loading policy

The terminal renders a compact placeholder that includes media type and encoded
size. Activating it fetches the digest URL and opens the normal image preview.
The fetched response is browser-cacheable, so replay and resize reuse it.

Initial policy is **click to load on every device**, with no network-type
heuristic or automatic image request. Normal-buffer references can use public
xterm decorations; alternate-buffer references and placements whose geometry
cannot be tracked safely use a session image tray. Both open a separate web
preview without unmounting the terminal. Automatic thumbnails are deferred
until on-demand loading has been validated on the physical phone.

The touch interaction must expose a comfortable target through the existing
mobile link/action-sheet flow and must not focus the terminal input.

## Consequences

### Positive

- Initial and repeated terminal attaches no longer retransmit base64 image
  payloads already represented by references.
- PTY redraws reuse image hashes and browser cache entries.
- Cellular users choose when to spend image bandwidth.
- No arbitrary filesystem capability is added.
- Local terminal and CLI attachment compatibility is unchanged.
- The same reference protocol can later support eager inline loading without a
  second backend design.

### Negative

- The runner gains an image-protocol parser/cache and two browser output forms.
- An evicted reference cannot be loaded and must show an expired state.
- A runner restart invalidates old in-memory references.
- Kitty file/shared-memory transports, Sixel, and iTerm2 remain on the existing
  raw path until separately implemented.
- Text-heavy checkpoints remain uncompressed; application-level checkpoint
  compression may still be valuable independently.

## Required invariants and tests

1. Non-browser attach bytes are identical to PTY output.
2. Removing references from a browser stream yields the same non-image bytes in
   the same order.
3. Multipart Kitty transmissions split at every possible input boundary produce
   one digest and one reference.
4. Malformed, unsupported, and over-limit input passes through unchanged.
5. Repeated payloads occupy one cache entry and refresh LRU order.
6. Cache eviction is deterministic and byte-bounded.
7. Image GET rejects malformed digests and cannot enumerate or access paths.
8. Local and peer-owned image GETs return identical bodies and cache headers.
9. A browser-level test asserts that initial attach transfers references, not
   the original base64 payload, and that tapping loads the image once.
10. A recorded real Pi image stream remains the regression corpus for replay,
    resize, and reconnect behavior.

## Alternatives considered

### Re-enable WebSocket compression

Rejected. It regresses the known mobile reconnect failure and does not eliminate
repeated image transfer.

### Browser-only image hiding

Rejected. It saves rendering work but no network bytes.

### Serve the path printed by the tool

Rejected. It exposes filesystem authority, fails for remote peers unless paths
are reinterpreted, and is unnecessary when the runner already receives the
actual emitted bytes.

### Persist extracted images in gmuxd

Deferred. Durable storage and cleanup are unnecessary for the first phase.
Content addressing permits a later disk cache without changing browser
references.

### Keep one always-on browser stream

Insufficient by itself. It can reduce reconnect replay but does not help the
first cellular attach, browser reloads, or PTY redraw image retransmission.
