import type { Terminal } from '@xterm/xterm'
import { WebglAddon } from '@xterm/addon-webgl'

/** Prefer xterm's upstream WebGL renderer and fall back to DOM when unavailable. */
export function loadWebglRenderer(term: Terminal): void {
  try {
    term.loadAddon(new WebglAddon())
  } catch {
    // WebGL may be disabled or blocklisted; xterm keeps its DOM renderer.
  }
}
