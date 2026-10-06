import { Archive, ArchiveRestore, Bell, Bot, Check, CircleCheck, CircleDot, Settings, Trash2 } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert } from '../components';
import { timeAgo } from '../lib/format';
import { describeError } from '../lib/http';
import {
  fetchNotifications,
  markAllRead,
  notifyNotificationsChanged,
  removeNotification,
  setNotificationArchived,
  setNotificationRead,
} from '../lib/notificationsApi';
import type { Notification, NotificationList, NotificationReason, NotificationState, ReasonFilter } from '../lib/notificationsApi';

const REASON_FILTERS: { value: ReasonFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'mentioned', label: 'Mentioned' },
  { value: 'assigned', label: 'Assigned' },
  { value: 'participating', label: 'Participating' },
  { value: 'webhook', label: 'Webhooks' },
];

const REASON_LABEL: Record<NotificationReason, string> = {
  assigned: 'Assigned',
  mentioned: 'Mention',
  participating: 'Participating',
  subscribed: 'Subscribed',
  commit_linked: 'Commit',
  state_change: 'State change',
  webhook: 'Webhook',
  mirror: 'Mirror',
};

function initials(name: string): string {
  return name.slice(0, 2).toUpperCase();
}

function NotificationRow({
  n,
  busy,
  onRead,
  onArchive,
  onDelete,
}: {
  n: Notification;
  busy: boolean;
  onRead: (n: Notification) => void;
  onArchive: (n: Notification) => void;
  onDelete: (n: Notification) => void;
}) {
  const repo = n.repository?.fullName;
  const issueHref = repo && n.issue ? `/${repo}/issues/${n.issue.number}` : null;
  const actor = n.actor;
  const isAgent = actor?.kind === 'agent';
  const classes = ['list-row', 'hover', n.read ? '' : 'unread'].filter(Boolean).join(' ');

  return (
    <li className={classes} data-testid="notification">
      {actor ? (
        <span className={`avatar${isAgent ? ' av-bot' : ''}`} aria-hidden="true">
          {isAgent ? <Bot size={14} /> : initials(actor.username)}
        </span>
      ) : n.issue?.state === 'closed' ? (
        <CircleCheck size={18} style={{ color: 'var(--closed)', margin: 4 }} aria-hidden="true" />
      ) : (
        <CircleDot size={18} style={{ margin: 4 }} aria-hidden="true" />
      )}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div>
          {actor ? (
            <>
              <b>{actor.username}</b>{' '}
              {isAgent ? (
                <>
                  <span className="badge badge-agent">agent</span>{' '}
                </>
              ) : null}
            </>
          ) : null}
          <span className={actor ? undefined : 'muted'}>{n.summary}</span>
          {issueHref && n.issue ? (
            <>
              {' '}
              <Link to={issueHref}>{n.issue.title}</Link>
            </>
          ) : null}
        </div>
        <div className="small muted">
          {repo ? <span className="mono">{repo}</span> : null}
          {n.issue ? <span> #{n.issue.number}</span> : null}
          {n.webhook ? <span className="mono"> {n.webhook.url}</span> : null}
        </div>
      </div>
      <span className="badge">{REASON_LABEL[n.reason]}</span>
      {n.archived ? <span className="badge">Archived</span> : null}
      <span className="small subtle">{timeAgo(n.createdAt)}</span>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        aria-label={n.read ? `Mark as unread: ${n.summary}` : `Mark as read: ${n.summary}`}
        title={n.read ? 'Mark as unread' : 'Mark as read'}
        onClick={() => onRead(n)}
      >
        <Check size={14} aria-hidden="true" />
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        aria-label={n.archived ? `Restore: ${n.summary}` : `Archive: ${n.summary}`}
        title={n.archived ? 'Restore' : 'Archive'}
        onClick={() => onArchive(n)}
      >
        {n.archived ? <ArchiveRestore size={14} aria-hidden="true" /> : <Archive size={14} aria-hidden="true" />}
      </Button>
      <Button size="sm" variant="ghost" disabled={busy} aria-label={`Delete: ${n.summary}`} title="Delete" onClick={() => onDelete(n)}>
        <Trash2 size={14} aria-hidden="true" />
      </Button>
    </li>
  );
}

// Centro notifiche (mockup 05): casella Unread/All, filtro per motivo,
// segna come letta, archivia, elimina (C9) e "Mark all as read".
export function NotificationsPage() {
  const [state, setState] = useState<NotificationState>('unread');
  const [reason, setReason] = useState<ReasonFilter>('all');
  const [list, setList] = useState<NotificationList | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [marking, setMarking] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setList(await fetchNotifications(state, reason));
    } catch (err) {
      setList(null);
      setError(describeError(err));
    } finally {
      setLoading(false);
    }
  }, [state, reason]);

  useEffect(() => {
    void load();
  }, [load]);

  async function run(id: string | null, action: () => Promise<unknown>) {
    setError(null);
    if (id) setBusyId(id);
    else setMarking(true);
    try {
      await action();
      notifyNotificationsChanged();
      await load();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusyId(null);
      setMarking(false);
    }
  }

  const unread = list?.unreadCount ?? 0;
  const items = list?.items ?? [];

  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <div className="page-h">
        <h1>Notifications</h1>
        <span className="sp" />
        <span className="seg" role="group" aria-label="Inbox">
          <button type="button" className={state === 'unread' ? 'active' : ''} aria-pressed={state === 'unread'} onClick={() => setState('unread')}>
            Unread{unread > 0 ? ` ${unread}` : ''}
          </button>
          <button type="button" className={state === 'all' ? 'active' : ''} aria-pressed={state === 'all'} onClick={() => setState('all')}>
            All
          </button>
        </span>
        <Button size="sm" disabled={marking || unread === 0} onClick={() => void run(null, () => markAllRead(reason))}>
          <Check size={14} aria-hidden="true" />
          Mark all as read
        </Button>
        <Link className="btn btn-sm" to="/settings/notifications">
          <Settings size={14} aria-hidden="true" />
          Settings
        </Link>
      </div>

      <div className="row small" style={{ gap: 8 }}>
        <span className="muted">Reason:</span>
        <span className="seg" role="group" aria-label="Reason">
          {REASON_FILTERS.map((f) => (
            <button key={f.value} type="button" className={reason === f.value ? 'active' : ''} aria-pressed={reason === f.value} onClick={() => setReason(f.value)}>
              {f.label}
            </button>
          ))}
        </span>
      </div>

      {error ? <ErrorAlert message={error} /> : null}
      {loading && !list ? <p className="muted">Loading…</p> : null}

      {list && items.length === 0 ? (
        <EmptyState
          icon={<Bell size={28} aria-hidden="true" />}
          title={state === 'unread' ? "You're all caught up" : 'No notifications'}
          description={
            state === 'unread'
              ? 'No unread notifications for this filter.'
              : 'Mentions, assignments and activity on issues you follow show up here.'
          }
        />
      ) : null}

      {items.length > 0 ? (
        <ul className="list notif" aria-label="Notifications">
          {items.map((n) => (
            <NotificationRow
              key={n.id}
              n={n}
              busy={busyId === n.id}
              onRead={(x) => void run(x.id, () => setNotificationRead(x.id, !x.read))}
              onArchive={(x) => void run(x.id, () => setNotificationArchived(x.id, !x.archived))}
              onDelete={(x) => void run(x.id, () => removeNotification(x.id))}
            />
          ))}
        </ul>
      ) : null}
    </div>
  );
}
