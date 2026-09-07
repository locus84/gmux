import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  refreshWebPushState,
  setWebPushProject,
  webPushBusy,
  webPushEnabled,
  webPushError,
  webPushPendingProjectSlug,
  webPushProjectSlugs,
  webPushSupported,
} from './push-subscriptions'

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

function stubPushBrowser(getSubscription: () => Promise<PushSubscription | null>): void {
  const pushManager = { getSubscription }
  vi.stubGlobal('window', {
    isSecureContext: true,
    PushManager: class {},
    Notification: class {},
  })
  vi.stubGlobal('PushManager', class {})
  vi.stubGlobal('Notification', class {})
  vi.stubGlobal('navigator', {
    serviceWorker: { ready: Promise.resolve({ pushManager }) },
    userAgent: 'vitest',
  })
}

describe('web push state coordination', () => {
  beforeEach(() => {
    webPushSupported.value = false
    webPushEnabled.value = false
    webPushProjectSlugs.value = new Set()
    webPushBusy.value = false
    webPushPendingProjectSlug.value = null
    webPushError.value = null
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('marks only the project being updated as pending', async () => {
    const patch = deferred<Response>()
    const subscription = { endpoint: 'https://push.example/sub' } as PushSubscription
    stubPushBrowser(async () => subscription)
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.method).toBe('PATCH')
      return patch.promise
    }))
    webPushEnabled.value = true

    const update = setWebPushProject('alpha', true)
    await vi.waitFor(() => expect(webPushPendingProjectSlug.value).toBe('alpha'))

    expect(webPushBusy.value).toBe(true)
    patch.resolve(new Response(null, { status: 200 }))
    await update

    expect(webPushBusy.value).toBe(false)
    expect(webPushPendingProjectSlug.value).toBe(null)
    expect(webPushProjectSlugs.value).toEqual(new Set(['alpha']))
  })

  it('serializes project updates so concurrent toggles merge instead of overwriting', async () => {
    const firstPatch = deferred<Response>()
    const secondPatch = deferred<Response>()
    const subscription = { endpoint: 'https://push.example/sub' } as PushSubscription
    stubPushBrowser(async () => subscription)
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.method).toBe('PATCH')
      return fetchMock.mock.calls.length === 1 ? firstPatch.promise : secondPatch.promise
    })
    vi.stubGlobal('fetch', fetchMock)
    webPushEnabled.value = true

    const alpha = setWebPushProject('alpha', true)
    const beta = setWebPushProject('beta', true)
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(webPushPendingProjectSlug.value).toBe('alpha')

    firstPatch.resolve(new Response(null, { status: 200 }))
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(webPushPendingProjectSlug.value).toBe('beta')
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string).projects).toEqual(['alpha', 'beta'])

    secondPatch.resolve(new Response(null, { status: 200 }))
    await Promise.all([alpha, beta])
    expect(webPushBusy.value).toBe(false)
    expect(webPushPendingProjectSlug.value).toBe(null)
    expect(webPushProjectSlugs.value).toEqual(new Set(['alpha', 'beta']))
  })

  it('retains an actionable enable error while Settings refreshes an absent subscription', async () => {
    stubPushBrowser(async () => null)
    webPushError.value = 'Notifications are blocked for this site. Enable them in browser settings first.'

    await refreshWebPushState()

    expect(webPushError.value).toBe('Notifications are blocked for this site. Enable them in browser settings first.')
  })

  it('clears a transient lookup error after a later successful lookup', async () => {
    const subscription = { endpoint: 'https://push.example/sub' } as PushSubscription
    stubPushBrowser(async () => subscription)
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: { found: true, subscription: { projects: ['alpha'] } },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    webPushError.value = 'Could not load push subscription.'

    await refreshWebPushState()

    expect(webPushError.value).toBeNull()
    expect(webPushProjectSlugs.value).toEqual(new Set(['alpha']))
  })

  it('ignores a lookup response that became stale during a project update', async () => {
    const lookup = deferred<Response>()
    const subscription = { endpoint: 'https://push.example/sub' } as PushSubscription
    stubPushBrowser(async () => subscription)
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') return lookup.promise
      return new Response(null, { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    const refresh = refreshWebPushState()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    webPushEnabled.value = true
    await setWebPushProject('alpha', true)
    expect(webPushProjectSlugs.value).toEqual(new Set(['alpha']))

    lookup.resolve(new Response(JSON.stringify({
      data: { found: true, subscription: { projects: [] } },
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    await refresh

    expect(webPushEnabled.value).toBe(true)
    expect(webPushProjectSlugs.value).toEqual(new Set(['alpha']))
  })
})
