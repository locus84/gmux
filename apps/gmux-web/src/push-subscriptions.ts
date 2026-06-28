import { signal } from '@preact/signals'

interface PushKeys {
  auth: string
  p256dh: string
}

interface StoredPushSubscription {
  id: string
  endpoint: string
  keys: PushKeys
  projects: string[]
}

interface PushSubscriptionJSON {
  endpoint?: string
  keys?: Partial<PushKeys>
}

export const webPushSupported = signal(false)
export const webPushEnabled = signal(false)
export const webPushProjectSlugs = signal<Set<string>>(new Set())
export const webPushBusy = signal(false)
export const webPushError = signal<string | null>(null)

export function isWebPushAvailable(): boolean {
  return window.isSecureContext
    && 'serviceWorker' in navigator
    && 'PushManager' in window
    && 'Notification' in window
}

export async function refreshWebPushState(): Promise<void> {
  webPushSupported.value = isWebPushAvailable()
  webPushError.value = null
  if (!webPushSupported.value) {
    webPushEnabled.value = false
    webPushProjectSlugs.value = new Set()
    return
  }

  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    if (!sub) {
      webPushEnabled.value = false
      webPushProjectSlugs.value = new Set()
      return
    }
    const endpoint = sub.endpoint
    const resp = await fetch('/v1/push/lookup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint }),
    })
    if (!resp.ok) throw new Error(`lookup failed: ${resp.status}`)
    const body = await resp.json() as { data?: { found?: boolean; subscription?: StoredPushSubscription } }
    const stored = body.data?.subscription
    webPushEnabled.value = !!body.data?.found
    webPushProjectSlugs.value = new Set(stored?.projects ?? [])
  } catch (err) {
    console.warn('web push state refresh failed:', err)
    webPushEnabled.value = false
    webPushProjectSlugs.value = new Set()
    webPushError.value = 'Could not load push subscription.'
  }
}

export async function enableWebPushForProjects(projectSlugs: string[]): Promise<void> {
  if (!isWebPushAvailable()) {
    webPushSupported.value = false
    webPushError.value = 'Web Push is not supported in this browser.'
    return
  }

  webPushBusy.value = true
  webPushError.value = null
  try {
    if (Notification.permission === 'denied') {
      webPushEnabled.value = false
      webPushError.value = 'Notifications are blocked for this site. Enable them in browser settings first.'
      return
    }

    const permission = await Notification.requestPermission()
    if (permission !== 'granted') {
      webPushEnabled.value = false
      webPushError.value = 'Notifications were not allowed.'
      return
    }

    const reg = await navigator.serviceWorker.ready
    let sub = await reg.pushManager.getSubscription()
    if (!sub) {
      const publicKey = await fetchVapidPublicKey()
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToArrayBuffer(publicKey),
      })
    }
    await saveSubscription(sub, projectSlugs)
  } catch (err) {
    console.warn('web push enable failed:', err)
    webPushError.value = describeWebPushError(err, 'Could not enable Web Push.')
  } finally {
    webPushBusy.value = false
  }
}

export async function setWebPushProject(slug: string, enabled: boolean): Promise<void> {
  const next = new Set(webPushProjectSlugs.value)
  if (enabled) next.add(slug)
  else next.delete(slug)

  if (!webPushEnabled.value) {
    await enableWebPushForProjects([...next])
    return
  }

  webPushBusy.value = true
  webPushError.value = null
  try {
    const sub = await currentSubscription()
    if (!sub) {
      await enableWebPushForProjects([...next])
      return
    }
    const resp = await fetch('/v1/push/subscription', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: sub.endpoint, projects: [...next] }),
    })
    if (!resp.ok) throw new Error(`update failed: ${resp.status}`)
    webPushEnabled.value = true
    webPushProjectSlugs.value = next
  } catch (err) {
    console.warn('web push project update failed:', err)
    webPushError.value = describeWebPushError(err, 'Could not update push projects.')
  } finally {
    webPushBusy.value = false
  }
}

async function currentSubscription(): Promise<PushSubscription | null> {
  if (!isWebPushAvailable()) return null
  const reg = await navigator.serviceWorker.ready
  return reg.pushManager.getSubscription()
}

async function fetchVapidPublicKey(): Promise<string> {
  const resp = await fetch('/v1/push/vapid-public-key')
  if (resp.status === 404) throw new Error('push_api_missing')
  if (!resp.ok) throw new Error(`public key failed: ${resp.status}`)
  const body = await resp.json() as { data?: { public_key?: string } }
  if (!body.data?.public_key) throw new Error('missing public key')
  return body.data.public_key
}

async function saveSubscription(sub: PushSubscription, projectSlugs: string[]): Promise<void> {
  const json = sub.toJSON() as PushSubscriptionJSON
  if (!json.endpoint || !json.keys?.auth || !json.keys.p256dh) {
    throw new Error('incomplete push subscription')
  }
  const resp = await fetch('/v1/push/subscribe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      subscription: {
        endpoint: json.endpoint,
        keys: {
          auth: json.keys.auth,
          p256dh: json.keys.p256dh,
        },
      },
      projects: projectSlugs,
      device_label: navigator.userAgent,
    }),
  })
  if (resp.status === 404) throw new Error('push_api_missing')
  if (!resp.ok) throw new Error(`subscribe failed: ${resp.status}`)
  const body = await resp.json() as { data?: StoredPushSubscription }
  webPushEnabled.value = true
  webPushProjectSlugs.value = new Set(body.data?.projects ?? projectSlugs)
}

export function localProjectSlugs(projects: { slug: string; peer?: string }[]): string[] {
  return projects.filter(p => !p.peer).map(p => p.slug)
}

function describeWebPushError(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : String(err)
  if (message === 'push_api_missing') {
    return 'This gmuxd has no Web Push API yet. Reinstall/restart gmuxd, then try again.'
  }
  if (message.includes('NotAllowedError')) {
    return 'Notifications are blocked for this site. Enable them in browser settings first.'
  }
  if (message.includes('AbortError') || message.includes('NotSupportedError')) {
    return 'Web Push is unavailable here. On iOS/iPadOS, open gmux from an HTTPS Home Screen PWA.'
  }
  return fallback
}

function urlBase64ToArrayBuffer(base64String: string): ArrayBuffer {
  const padding = '='.repeat((4 - base64String.length % 4) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const rawData = atob(base64)
  const buffer = new ArrayBuffer(rawData.length)
  const outputArray = new Uint8Array(buffer)
  for (let i = 0; i < rawData.length; ++i) {
    outputArray[i] = rawData.charCodeAt(i)
  }
  return buffer
}
