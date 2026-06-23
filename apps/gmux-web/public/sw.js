// Minimal service worker for installability.
// gmux is an online/live terminal app, so this intentionally does not cache or
// synthesize responses. Keeping a fetch listener preserves compatibility with
// Chromium versions that still require one for PWA install prompts while all
// requests continue to hit the daemon normally.
self.addEventListener('fetch', () => {
  // No respondWith(): use the browser's default network handling.
})
