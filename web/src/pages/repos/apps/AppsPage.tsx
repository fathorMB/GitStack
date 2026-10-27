import { AlertTriangle, Box, Check, Lock, Plus, Rocket, ShieldCheck, Terminal, Users, X } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ErrorAlert } from '../../../components';
import { fetchApps } from '../../../lib/appsApi';
import type { App, AppList } from '../../../lib/appsApi';
import { accessLabel, formatCpu, formatMemory, shortDigest, statusBadge } from '../../../lib/appsView';
import { timeAgo } from '../../../lib/format';
import type { Repository } from '../../../lib/reposApi';
import { useAppPermissions } from '../../../lib/useAppPermissions';
import { useLoad } from '../../../lib/useLoad';
import { appsPath } from './appMode';

/** Quota dell'owner: garantito usato / totale (H11), solo se il contratto la manda. */
function quotaText(quota: AppList['quota']): string | null {
  if (!quota) return null;
  const { used, limits } = quota;
  return `Quota ${quota.owner}: ${formatCpu(used.cpuMillis)} / ${formatCpu(limits.cpuMillis)} vCPU guaranteed · ${formatMemory(used.memoryBytes)} / ${formatMemory(limits.memoryBytes)} RAM guaranteed`;
}

function AppRow({ app, owner, repo }: { app: App; owner: string; repo: string }) {
  const badge = statusBadge(app.status, app.runningPreviousVersion);
  const bad = badge.tone === 'err';
  const sha = shortDigest(app.image.digest);
  const copiesText = `${app.copies} ${app.copies === 1 ? 'copy' : 'copies'}`;
  return (
    <li className="list-row hover">
      <span className={`st ${bad ? 'err' : badge.tone === 'ok' ? 'ok' : 'skip'}`}>{bad ? <X size={12} aria-hidden="true" /> : <Check size={12} aria-hidden="true" />}</span>
      <div style={{ flex: 1 }}>
        <Link to={appsPath(owner, repo, app.name)} style={{ fontWeight: 600, color: 'var(--text)' }}>
          {app.name}
        </Link>{' '}
        {app.address ? null : <span className="badge">No address</span>}
        <span className="badge">
          {app.access === 'login' ? <Lock size={12} aria-hidden="true" /> : <Users size={12} aria-hidden="true" />}
          {accessLabel(app.access)}
        </span>
        <div className="small muted">
          {app.address ? <span className="mono">{app.address}</span> : null}
          {app.address ? ' · ' : null}
          {app.follow ? (
            <>
              follows {app.follow.tagPattern ? 'tags ' : ''}<span className="mono">{app.follow.tagPattern ?? app.follow.branch}</span>
              {app.follow.paused ? ' (paused)' : ''}
            </>
          ) : (
            'manual deploys'
          )}
          {' · '}v{app.activeVersion}
          {sha ? (
            <>
              {' '}
              <span className="sha">{sha}</span>
            </>
          ) : null}
          {app.image.source === 'external' ? (
            <>
              {' · '}
              <span className="mono">{app.image.reference}</span>
            </>
          ) : null}
          {' · '}
          {copiesText}
          {app.image.runsAsRoot ? ' · runs as root' : ''}
          {app.lastDeploy ? (
            <>
              {' · deployed '}
              {timeAgo(app.lastDeploy.startedAt)}
            </>
          ) : null}
        </div>
      </div>
      {app.image.verified ? (
        <span className="badge badge-ok">
          <ShieldCheck size={12} aria-hidden="true" />
          verified
        </span>
      ) : (
        <span className="badge badge-run">
          <AlertTriangle size={12} aria-hidden="true" />
          not verified
        </span>
      )}
      <span className={badge.tone === 'none' ? 'badge' : `badge badge-${badge.tone}`}>{badge.label}</span>
    </li>
  );
}

/** Scheda Apps del repo (H1, mockup data-screen="apps"): elenco e variante «senza app». */
export function AppsPage({ repo }: { repo: Repository }) {
  const [version, setVersion] = useState(0);
  return <AppsLoaded key={version} repo={repo} reload={() => setVersion((v) => v + 1)} />;
}

function AppsLoaded({ repo }: { repo: Repository; reload: () => void }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const perms = useAppPermissions(repo);
  const { data, loading, error } = useLoad(() => fetchApps(owner, name));
  const [filter, setFilter] = useState('');

  if (loading && data === null) return <p className="muted">Loading apps…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Could not load apps.'} />;

  const items = data.items.filter((a) => a.name.toLowerCase().includes(filter.trim().toLowerCase()));
  const quota = quotaText(data.quota);

  if (data.items.length === 0) {
    return (
      <div className="card card-b" style={{ textAlign: 'center', padding: '48px 24px' }}>
        <Rocket size={40} aria-hidden="true" style={{ color: 'var(--accent)' }} />
        <h2 style={{ fontSize: 18, margin: '12px 0 6px' }}>Run this repository as an app</h2>
        <p className="muted" style={{ maxWidth: 520, margin: '0 auto 18px' }}>
          Deploy an image from this repository&apos;s packages or any registry. Apps start internal: only people who can reach GitStack, and by default only those with read access, can open them.
        </p>
        <div className="row" style={{ justifyContent: 'center' }}>
          {perms.canCreate ? (
            <Link className="btn btn-primary" to={appsPath(owner, name, 'new')}>
              <Plus size={16} aria-hidden="true" />
              New app
            </Link>
          ) : null}
          <span className="btn" aria-label="Command line">
            <Terminal size={16} aria-hidden="true" />
            gs app create
          </span>
        </div>
        {perms.ready && !perms.canCreate ? <p className="hint">Creating an app requires admin on the repository.</p> : null}
      </div>
    );
  }

  return (
    <div>
      <div className="row" style={{ marginBottom: 14 }}>
        <div className="input-icon" style={{ flex: 1, maxWidth: 360 }}>
          <Box size={16} aria-hidden="true" />
          <input className="input" placeholder="Filter apps" aria-label="Filter apps" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <span className="sp" />
        {perms.canCreate ? (
          <Link className="btn btn-primary" to={appsPath(owner, name, 'new')}>
            <Plus size={16} aria-hidden="true" />
            New app
          </Link>
        ) : null}
      </div>
      <div className="list">
        <div className="list-row list-head">
          <b style={{ color: 'var(--text)' }}>
            {data.items.length} {data.items.length === 1 ? 'app' : 'apps'}
          </b>
          <span className="sp" />
          {quota ? <span className="small">{quota}</span> : null}
        </div>
        <ul aria-label="Apps" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {items.map((a) => (
            <AppRow key={a.id} app={a} owner={owner} repo={name} />
          ))}
        </ul>
        {items.length === 0 ? <p className="muted" style={{ padding: 16 }}>No app matches “{filter}”.</p> : null}
      </div>
      <p className="hint" style={{ marginTop: 10 }}>
        Apps follow the repository: archiving stops them; deleting stops them and removes them with their volumes after 7 days unless the repository is restored.
      </p>
    </div>
  );
}
