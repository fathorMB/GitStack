import { Globe, HardDrive, Key } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ConfirmDialog, ErrorAlert } from '../../../components';
import { fetchAppAccess, saveAppAccess } from '../../../lib/appsApi';
import type { App, AppAccess, AppAccessMode } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';
import { AppAccessList } from './AppAccessList';
import { AppDomainsSection } from './AppDomainsSection';
import { AppHeader } from './AppHeader';
import { appsPath } from './appMode';

interface Props {
  repo: Repository;
  app: App;
  perms: AppPermissions;
  onChange: (app: App) => void;
}

/** Access & domains (H7, mockup data-screen="access", varianti interna e «pub»): modo di accesso, lista di accesso e domini propri (v3-c). */
export function AppAccessPage({ repo, app, perms, onChange }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data, loading, error, setData } = useLoad(() => fetchAppAccess(owner, name, app.name));
  const [saveError, setSaveError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmPublic, setConfirmPublic] = useState(false);

  async function choose(mode: AppAccessMode) {
    if (!data || mode === data.mode) return;
    setBusy(true);
    setSaveError(null);
    try {
      const next: AppAccess = await saveAppAccess(owner, name, app.name, mode);
      setData(next);
      onChange({ ...app, access: next.mode, address: next.address ?? app.address });
    } catch (err) {
      setSaveError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const publicOff = data ? !data.publicAppsEnabled : true;
  const disabled = !perms.canChangeAccess || busy;
  const opt = (mode: AppAccessMode, label: string, hint: React.ReactNode, extra?: React.ReactNode, off = false) => {
    const on = data?.mode === mode;
    return (
      <label className={`opt${on ? ' on' : ''}${off ? ' off' : ''}`}>
        <input type="radio" name="acc" aria-label={label} checked={on} disabled={disabled || off} onChange={() => (mode === 'public' ? setConfirmPublic(true) : void choose(mode))} />
        <div>
          <b>{label}</b> {extra}
          <div className="hint">{hint}</div>
        </div>
      </label>
    );
  };

  return (
    <>
      <AppHeader repo={repo} app={app} mode="app-access" />
      <div className="page">
        <div className="settings-grid">
          <nav className="subnav" aria-label="App settings">
            <Link to={appsPath(owner, name, app.name, 'settings')}>
              <Key size={16} aria-hidden="true" />
              Variables &amp; secrets
            </Link>
            <Link className="active" to={appsPath(owner, name, app.name, 'settings/access')} aria-current="page">
              <Globe size={16} aria-hidden="true" />
              Access &amp; domains
            </Link>
            <Link to={appsPath(owner, name, app.name, 'settings/volumes')}>
              <HardDrive size={16} aria-hidden="true" />
              Volumes &amp; resources
            </Link>
          </nav>
          <div className="stack" style={{ gap: 24 }}>
            {loading && data === null ? <p className="muted">Loading access…</p> : null}
            {error ? <ErrorAlert message={error} /> : null}
            {saveError ? <ErrorAlert message={saveError} /> : null}
            {data ? (
              <>
                <div>
                  <div className="sec-h">
                    <h2>Who can open this app</h2>
                    <span className="badge">admin only</span>
                  </div>
                  <div className="stack" style={{ gap: 8 }} role="radiogroup" aria-label="Who can open this app">
                    {opt(
                      'login',
                      'Internal · GitStack login',
                      <>
                        People with read access to <b>{owner}/{name}</b> sign in with GitStack. The app receives the user&apos;s name, email and teams in signed headers. Agents use a token with read access.
                      </>,
                      <span className="badge">default</span>,
                    )}
                    {opt('none', 'Internal · no login', 'Anyone who can reach the GitStack network opens it. Use only for apps with their own login.')}
                    {opt(
                      'public',
                      'Public on the internet',
                      publicOff ? (
                        'An installation administrator must first allow public apps in System · Apps.'
                      ) : (
                        <>
                          Anyone on the internet can open it; GitStack login does not apply. Allowed on this instance by <b>admin</b>
                          {data.mode === 'public' && data.changedBy?.username ? <>; chosen by <b>{data.changedBy.username}</b>{chosenOn(data.changedAt)}</> : null}.
                        </>
                      ),
                      publicOff ? <span className="badge">disabled by the administrator</span> : null,
                      publicOff && data.mode !== 'public',
                    )}
                  </div>
                  {!perms.canChangeAccess ? <p className="hint">Only admins of the repository can change who opens the app.</p> : null}
                  <p className="hint">
                    <a href="https://fathormb.github.io/GitStack/user/publish-an-app/" target="_blank" rel="noreferrer">Guide: publish an app on your own domain</a>
                  </p>
                </div>
                {data.mode !== 'public' && perms.canChangeAccess ? <AppAccessList repo={repo} app={app} canEdit /> : null}
                {data.mode !== 'public' && !perms.canChangeAccess ? (
                  <p className="hint">The access list is managed by admins of the repository.</p>
                ) : null}
                <AppDomainsSection repo={repo} app={app} access={data} canEdit={perms.canChangeAccess} />
              </>
            ) : null}
          </div>
        </div>
      </div>
      <ConfirmDialog
        open={confirmPublic}
        onOpenChange={setConfirmPublic}
        title={`Make ${app.name} public?`}
        description="Anyone on the internet will be able to open this app; GitStack login will not apply and the access list is not used. You can switch back at any time."
        confirmLabel="Make public"
        destructive
        onConfirm={() => void choose('public')}
      />
    </>
  );
}

function chosenOn(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : ` on ${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}`;
}
