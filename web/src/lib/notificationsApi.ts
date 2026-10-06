// Notifiche in-app (M-06, C3/C9) via client generato (client di default:
// l'interceptor 401 le vede).
import {
  deleteNotification,
  getIssueSubscription,
  getNotificationPreferences,
  getRepoWatch,
  listNotifications,
  markAllNotificationsRead,
  setRepoWatch,
  subscribeIssue,
  unsubscribeIssue,
  updateNotification,
  updateNotificationPreferences,
} from '@gitstack/api-client';
import type { IssueSubscription, Notification, NotificationList, NotificationPreferences, NotificationReason, RepoWatch, RepoWatchMode } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { IssueSubscription, Notification, NotificationList, NotificationPreferences, NotificationReason, RepoWatch, RepoWatchMode };

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

// Preferenze email, Watch del repo e Subscribe della issue (M-06/A, C3-C5).
export async function fetchNotificationPreferences(): Promise<NotificationPreferences> {
  return unwrap(await getNotificationPreferences({ baseUrl: API_BASE_URL }));
}

export async function saveEmailPreferences(email: Record<string, boolean>): Promise<NotificationPreferences> {
  return unwrap(await updateNotificationPreferences({ baseUrl: API_BASE_URL, body: { email } }));
}

export async function fetchRepoWatch(owner: string, repo: string): Promise<RepoWatch> {
  return unwrap(await getRepoWatch({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function saveRepoWatch(owner: string, repo: string, mode: RepoWatchMode): Promise<RepoWatch> {
  return unwrap(await setRepoWatch({ baseUrl: API_BASE_URL, path: { owner, repo }, body: { mode } }));
}

export async function fetchIssueSubscription(owner: string, repo: string, number: number): Promise<IssueSubscription> {
  return unwrap(await getIssueSubscription({ baseUrl: API_BASE_URL, path: { owner, repo, number } }));
}

export async function subscribeToIssue(owner: string, repo: string, number: number): Promise<IssueSubscription> {
  return unwrap(await subscribeIssue({ baseUrl: API_BASE_URL, path: { owner, repo, number } }));
}

export async function unsubscribeFromIssue(owner: string, repo: string, number: number): Promise<IssueSubscription> {
  return unwrap(await unsubscribeIssue({ baseUrl: API_BASE_URL, path: { owner, repo, number } }));
}
