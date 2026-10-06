import { useEffect, useState } from 'react';
import { ErrorAlert, useToast } from '../../components';
import { describeError } from '../../lib/http';
import { fetchNotificationPreferences, saveEmailPreferences } from '../../lib/notificationsApi';
import type { NotificationPreferences } from '../../lib/notificationsApi';
import { fetchRepos } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { RepoWatchSelect } from '../repos/RepoWatchSelect';
import { useSettingsUser } from './SettingsLayout';

// Tipi del mockup 21 e i motivi API (NotificationReason) che ognuno raggruppa.
const TYPES: { key: string; title: string; hint: string; reasons: string[] }[] = [
  { key: 'mentions', title: 'Mentions', hint: 'Someone writes @you or a team you belong to', reasons: ['mentioned'] },
  { key: 'assignments', title: 'Assignments', hint: 'An issue is assigned to you', reasons: ['assigned'] },
  {
    key: 'activity',
    title: 'Activity on issues you follow',
    hint: 'Comments, closing and reopening, linked commits',
    reasons: ['participating', 'subscribed', 'commit_linked', 'state_change'],
  },
  { key: 'webhooks', title: 'Webhooks you manage', hint: 'A webhook was disabled after repeated failures', reasons: ['webhook'] },
];

// Impostazioni notifiche (mockup 21): email per tipo, Watch per repo, conservazione (C9).
export function NotificationSettingsPage() {
  const user = useSettingsUser();
  const { toast } = useToast();
  const [prefs, setPrefs] = useState<NotificationPreferences | null>(null);
  const [prefsError, setPrefsError] = useState<string | null>(null);
  const [repos, setRepos] = useState<Repository[] | null>(null);
  const [reposError, setReposError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    fetchNotificationPreferences().then(
      (p) => live && setPrefs(p),
      (err: unknown) => live && setPrefsError(describeError(err)),
    );
    fetchRepos().then(
      (r) => live && setRepos(r),
      (err: unknown) => live && setReposError(describeError(err)),
    );
    return () => {
      live = false;
    };
  }, []);

  const toggle = async (reasons: string[], on: boolean) => {
    if (!prefs) return;
    const before = prefs;
    const patch = Object.fromEntries(reasons.map((r) => [r, on]));
    setPrefs({ ...prefs, email: { ...prefs.email, ...patch } });
    try {
      setPrefs(await saveEmailPreferences(patch));
    } catch (err) {
      setPrefs(before);
      toast(describeError(err), 'error');
    }
  };

  return (
    <>
      <section aria-label="Notification preferences">
        <div className="sec-h">
          <h2>Notifications</h2>
        </div>
        <p className="muted" style={{ marginBottom: 16 }}>
          You always get in-app notifications.
          {prefs?.emailAvailable ? (
            <>
              {' '}
              Choose which ones should also reach you by email{user.email ? <> at <b>{user.email}</b></> : null}.
            </>
          ) : null}{' '}
          You never get notified about your own actions.
        </p>
        {prefsError ? <ErrorAlert message={prefsError} /> : null}
        {prefs?.emailAvailable ? (
          <div className="list" role="table" aria-label="Notification types">
            <div className="list-row list-head small" role="row" style={{ fontWeight: 600 }}>
              <span style={{ flex: 1 }}>Type</span>
              <span style={{ width: 80, textAlign: 'center' }}>In-app</span>
              <span style={{ width: 80, textAlign: 'center' }}>Email</span>
            </div>
            {TYPES.map((t) => (
              <div key={t.key} className="list-row" role="row">
                <div style={{ flex: 1 }}>
                  <b>{t.title}</b>
                  <div className="small muted">{t.hint}</div>
                </div>
                <span style={{ width: 80, textAlign: 'center' }}>
                  <input type="checkbox" checked disabled aria-label={`${t.title} in-app`} />
                </span>
                <span style={{ width: 80, textAlign: 'center' }}>
                  <input
                    type="checkbox"
                    aria-label={`${t.title} by email`}
                    checked={t.reasons.every((r) => prefs.email[r] === true)}
                    onChange={(e) => void toggle(t.reasons, e.target.checked)}
                  />
                </span>
              </div>
            ))}
          </div>
        ) : null}
        {prefs && !prefs.emailAvailable ? (
          <div className="alert alert-info" role="status">
            <div>
              Your administrator hasn't configured an email server (or this account can't receive email): notifications are shown in-app only, so the email options are hidden.
            </div>
          </div>
        ) : null}
      </section>

      <section aria-label="Watching">
        <div className="sec-h">
          <h2>Watching</h2>
        </div>
        <p className="muted" style={{ marginBottom: 12 }}>
          <b>Participating</b>: only issues you opened, are assigned to, comment on or are mentioned in. <b>All activity</b>: every new issue and comment. <b>Ignore</b>: nothing, not even mentions.
        </p>
        {reposError ? <ErrorAlert message={reposError} /> : null}
        {repos === null && !reposError ? <p className="muted">Loading…</p> : null}
        {repos && repos.length === 0 ? <p className="muted">No repositories yet.</p> : null}
        {repos && repos.length > 0 ? (
          <div className="list">
            {repos.map((r) => (
              <div key={r.id} className="list-row">
                <span className="mono" style={{ flex: 1, fontSize: 13 }}>
                  {r.owner.name}/{r.name}
                </span>
                <RepoWatchSelect owner={r.owner.name} repo={r.name} prefix={false} />
              </div>
            ))}
          </div>
        ) : null}
        <p className="small muted" style={{ marginTop: 8 }}>
          Every other repository you can see uses <b>Participating</b>.
        </p>
      </section>

      <section aria-label="Retention">
        <div className="sec-h">
          <h2>Retention</h2>
        </div>
        <p className="muted">
          Read notifications are removed after 90 days. Unread notifications are kept until you read them. Notifications from a repository you lose access to disappear.
        </p>
        <div className="cmdblock" style={{ marginTop: 10, fontSize: 12 }}>
          gs notification list --reason assigned,mentioned --json
        </div>
      </section>
    </>
  );
}
