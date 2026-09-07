/**
 * Presence hook: WebSocket connection to the daemon for notifications,
 * tab title badge, idle detection, and visibility/focus reporting.
 *
 * Reads selection and unread projections from the store (signals). The only
 * prop-driven input is the notification click callback (needs routing).
 */

import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { connectPresence } from './presence'
import type { NotifyMessage, CancelMessage } from './presence'
import { selectedId, unreadCount, navigateToSession, projects } from './store'
import type { NotifPermission } from './sidebar'
import { enableWebPushForProjects, localProjectSlugs, refreshWebPushState } from './push-subscriptions'

const USE_MOCK = import.meta.env.VITE_MOCK === '1' || location.search.includes('mock')

interface UsePresenceResult {
  notifPermission: NotifPermission
  requestNotifPermission: () => void
}

function showWindowNotification(msg: NotifyMessage, options: NotificationOptions, active: Map<string, Notification>) {
  const notification = new Notification(msg.title, options)
  active.set(msg.id, notification)
  notification.onclose = () => active.delete(msg.id)
  notification.onclick = () => {
    window.focus()
    if (msg.session_id) navigateToSession(msg.session_id)
    notification.close()
  }
}

export function usePresence(): UsePresenceResult {
  const activeNotifsRef = useRef<Map<string, Notification>>(new Map())
  const presenceRef = useRef<ReturnType<typeof connectPresence> | null>(null)
  const lastInteractionRef = useRef(Date.now() / 1000)

  const [, forceNotifPermUpdate] = useState(0)
  const notifPermission: NotifPermission = USE_MOCK
    ? 'granted'
    : ('Notification' in window ? Notification.permission : 'unavailable')

  const handleNotify = useCallback((msg: NotifyMessage) => {
    if (!('Notification' in window) || Notification.permission !== 'granted') return
    const options: NotificationOptions = {
      body: msg.body, tag: msg.tag, icon: '/favicon.svg',
      data: { id: msg.id, session_id: msg.session_id },
    }
    if ('serviceWorker' in navigator) {
      void navigator.serviceWorker.ready
        .then(reg => reg.showNotification(msg.title, options))
        .catch(() => showWindowNotification(msg, options, activeNotifsRef.current))
      return
    }
    showWindowNotification(msg, options, activeNotifsRef.current)
  }, [])

  // Dismiss a notification when the daemon tells us to.
  const handleCancel = useCallback((msg: CancelMessage) => {
    const n = activeNotifsRef.current.get(msg.id)
    if (n) { n.close(); activeNotifsRef.current.delete(msg.id) }
    if ('serviceWorker' in navigator) {
      void navigator.serviceWorker.ready.then(async reg => {
        for (const notification of await reg.getNotifications()) {
          if ((notification.data as { id?: string } | undefined)?.id === msg.id) notification.close()
        }
      }).catch(() => {})
    }
  }, [])

  // Connect presence WebSocket on mount.
  useEffect(() => {
    const p = connectPresence({ onNotify: handleNotify, onCancel: handleCancel })
    presenceRef.current = p
    void refreshWebPushState()
    return () => { p.close(); presenceRef.current = null }
  }, [handleNotify, handleCancel])

  useEffect(() => {
    if (!('serviceWorker' in navigator)) return
    const onMessage = (event: MessageEvent) => {
      const message = event.data as { type?: string; session_id?: string } | undefined
      if (message?.type === 'gmux-notification-click' && message.session_id) {
        window.focus()
        navigateToSession(message.session_id)
      }
    }
    navigator.serviceWorker.addEventListener('message', onMessage)
    return () => navigator.serviceWorker.removeEventListener('message', onMessage)
  }, [])

  // Track last user interaction for idle detection.
  useEffect(() => {
    const update = () => { lastInteractionRef.current = Date.now() / 1000 }
    const events = ['mousemove', 'keydown', 'touchstart', 'scroll'] as const
    events.forEach(e => { document.addEventListener(e, update, { passive: true }) })
    return () => events.forEach(e => { document.removeEventListener(e, update) })
  }, [])

  // Report state changes to the daemon.
  // Read signals inside the callback; useCallback has no deps since
  // signal reads are always current.
  const reportPresence = useCallback(() => {
    presenceRef.current?.sendState({
      visibility: document.visibilityState,
      focused: document.hasFocus(),
      selected_session_id: selectedId.value,
      last_interaction: lastInteractionRef.current,
    })
  }, [])

  // Report on visibility/focus changes + heartbeat.
  // Also re-report whenever selectedId changes.
  useEffect(() => {
    reportPresence()
  }, [selectedId.value, reportPresence])

  useEffect(() => {
    const report = () => reportPresence()
    document.addEventListener('visibilitychange', report)
    window.addEventListener('focus', report)
    window.addEventListener('blur', report)
    const heartbeat = setInterval(report, 30_000)
    return () => {
      document.removeEventListener('visibilitychange', report)
      window.removeEventListener('focus', report)
      window.removeEventListener('blur', report)
      clearInterval(heartbeat)
    }
  }, [reportPresence])

  // Tab title badge.
  useEffect(() => {
    const count = unreadCount.value
    document.title = count > 0 ? `(${count}) gmux` : 'gmux'
  }, [unreadCount.value])

  const requestNotifPermission = useCallback(async () => {
    await Notification.requestPermission()
    forceNotifPermUpdate(n => n + 1)
    presenceRef.current?.sendPermission(Notification.permission)
    if (Notification.permission === 'granted') {
      await enableWebPushForProjects(localProjectSlugs(projects.value))
    }
  }, [])

  return { notifPermission, requestNotifPermission }
}
