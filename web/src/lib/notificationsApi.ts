// Notifiche in-app (M-06, C3/C9) via client generato (client di default:
// l'interceptor 401 le vede).
import {
  deleteNotification,
  listNotifications,
  markAllNotificationsRead,
  updateNotification,
} from '@gitstack/api-client';
import type { Notification, NotificationList, NotificationReason } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { Notification, NotificationList, NotificationReason };

/** Filtro per motivo del mockup 05: i motivi API che ognuno raggruppa. */
export type ReasonFilter = 'all' | 'mentioned' | 'assigned' | 'participating' | 'webhook';

export type NotificationState = 'unread' | 'all';

const CHANGED = 'gitstack:notifications-changed';

/** Avvisa il contatore della barra che la casella e' cambiata. */
export function notifyNotificationsChanged(): void {
  window.dispatchEvent(new Event(CHANGED));
}

export function onNotificationsChanged(fn: () => void): () => void {
  window.addEventListener(CHANGED, fn);
  return () => window.removeEventListener(CHANGED, fn);
}

export async function fetchNotifications(state: NotificationState, reason: ReasonFilter, page = 1, perPage = 30): Promise<NotificationList> {
  return unwrap(
    await listNotifications({
      baseUrl: API_BASE_URL,
      query: { state, ...(reason !== 'all' ? { reason } : {}), page, perPage },
    }),
  );
}

/** Solo il numero delle non lette (contatore della barra). */
export async function fetchUnreadCount(): Promise<number> {
  return (await fetchNotifications('unread', 'all', 1, 1)).unreadCount;
}

export async function setNotificationRead(id: string, read: boolean): Promise<Notification> {
  return unwrap(await updateNotification({ baseUrl: API_BASE_URL, path: { notificationId: id }, body: { read } }));
}

export async function setNotificationArchived(id: string, archived: boolean): Promise<Notification> {
  return unwrap(await updateNotification({ baseUrl: API_BASE_URL, path: { notificationId: id }, body: { archived } }));
}

export async function removeNotification(id: string): Promise<void> {
  unwrapEmpty(await deleteNotification({ baseUrl: API_BASE_URL, path: { notificationId: id } }));
}

/** Segna come lette le non lette, con lo stesso filtro per motivo della vista. */
export async function markAllRead(reason: ReasonFilter): Promise<number> {
  return unwrap(
    await markAllNotificationsRead({ baseUrl: API_BASE_URL, ...(reason !== 'all' ? { query: { reason } } : {}) }),
  ).marked;
}
