import { Grid2x2 } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert } from '../components';
import { fetchUserApps } from '../lib/appsApi';
import type { App } from '../lib/appsApi';
import { listAddress, statusBadge } from '../lib/appsView';
import { useLoad } from '../lib/useLoad';

const PER_PAGE = 20;

function appHref(a: Pick<App, 'owner' | 'repo'>): string {
  return `/${encodeURIComponent(a.owner)}/${encodeURIComponent(a.repo)}/apps`;
}

function AppRow({ app }: { app: App }) {
  const badge = statusBadge(app.status, app.runningPreviousVersion);
  const address = listAddress(app);
  return (
    <li className="list-row hover" data-testid="user-app">
      <div className="grow">
        <Link to={appHref(app)} style={{ fontWeight: 600, color: 'var(--text)' }}>
          {app.owner}/{app.repo}
        </Link>
        <span className="muted"> · </span>
        <span>{app.name}</span>
        <div className="small muted">
          {address ? <span className="mono">{address}</span> : 'No address'}
          {' · '}v{app.activeVersion}
        </div>
      </div>
      <span className={badge.tone === 'none' ? 'badge' : `badge badge-${badge.tone}`}>{badge.label}</span>
    </li>
  );
}

function UserAppsList({ page, onPage }: { page: number; onPage: (p: number) => void }) {
  const { data, loading, error } = useLoad(() => fetchUserApps(page, PER_PAGE));
  if (loading && data === null) return <p className="muted">Loading apps…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Could not load apps.'} />;
  if (!data.hostingEnabled) {
    return (
      <EmptyState
        title="App hosting is turned off"
        description="The administrator has not enabled app hosting on this instance, so no app can run. Ask an administrator to turn it on."
      />
    );
  }
  if (data.total === 0) {
    return (
      <EmptyState
        title="No apps yet"
        description="None of the repositories you can read has an app. Open a repository and use its Apps tab to run one."
      />
    );
  }
  const pages = Math.max(1, Math.ceil(data.total / PER_PAGE));
  return (
    <>
      <ul className="list list-clean" aria-label="Apps">
        {data.items.map((a) => (
          <AppRow key={a.id} app={a} />
        ))}
      </ul>
      {pages > 1 ? (
        <div className="row section-gap">
          <Button type="button" disabled={page <= 1} onClick={() => onPage(page - 1)}>
            Previous
          </Button>
          <span className="muted small">
            Page {page} of {pages}
          </span>
          <Button type="button" disabled={page >= pages} onClick={() => onPage(page + 1)}>
            Next
          </Button>
        </div>
      ) : null}
    </>
  );
}

// Pagina Apps del menu (GIT-548): le app di tutti i repo che l'utente puo' leggere.
export function UserAppsPage() {
  const [page, setPage] = useState(1);
  return (
    <div>
      <div className="page-h">
        <Grid2x2 size={20} aria-hidden="true" />
        <h1>Apps</h1>
      </div>
      <UserAppsList key={page} page={page} onPage={setPage} />
    </div>
  );
}
