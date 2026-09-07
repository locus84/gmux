// Minimal service worker for installability and mobile notifications.
// gmux is an online/live terminal app, so this intentionally does not cache or
// synthesize responses. Keeping a fetch listener preserves compatibility with
// Chromium versions that still require one for PWA install prompts while all
// requests continue to hit the daemon normally.
self.addEventListener('fetch', () => {
  // No respondWith(): use the browser's default network handling.
})

function notificationOptions(payload) {
  return {
    body: payload.body || '',
    tag: payload.tag || payload.session_id || payload.id || 'gmux',
    icon: '/favicon.svg',
    badge: '/icon-192.png',
    data: {
      id: payload.id,
      session_id: payload.session_id,
      url: payload.url || '/',
    },
  }
}

self.addEventListener('push', (event) => {
  event.waitUntil((async () => {
    if (!event.data) return
    let payload
    try {
      payload = event.data.json()
    } catch {
      payload = { title: 'gmux', body: event.data.text() }
    }
    const title = payload.title || 'gmux'
    await self.registration.showNotification(title, notificationOptions(payload))
  })())
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()

  const data = event.notification.data || {}
  const sessionId = data.session_id
  const url = data.url || '/'
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    for (const client of windows) {
      client.postMessage({ type: 'gmux-notification-click', session_id: sessionId })
      if ('focus' in client) return client.focus()
    }
    return self.clients.openWindow(url)
  })())
})
