import { Check, ShieldCheck, Undo2, X } from 'lucide-react';
import { useState } from 'react';
import { Button, ConfirmDialog, ErrorAlert } from '../../../components';
import { fetchApp, fetchAppVersions, rollbackApp } from '../../../lib/appsApi';
import type { App, AppVersion } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { shortDigest } from '../../../lib/appsView';
import { timeAgo } from '../../../lib/format';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';
import { AppHeader } from './AppHeader';

interface Props {
  repo: Repository;
  app: App;
  perms: AppPermissions;
  onChange: (app: App) => void;
}

function describeTrigger(v: AppVersion, follow: string | undefined): string {
  const who = v.createdBy?.username;
  switch (v.trigger) {
    case 'branch_push':
      return `push to ${v.branch ?? follow ?? 'the branch'}${who ? ` by ${who}` : ''}`;
    case 'tag_push':
      return `tag ${v.image.reference.split(':').pop() ?? ''}${who ? ` pushed by ${who}` : ''}`;
    case 'manual':
      return `manual deploy${who ? ` by ${who}` : ''}`;
    case 'config':
      return `${v.summary ?? 'configuration change'}${who ? ` by ${who}` : ''}`;
    case 'rollback':
      return `rollback${v.rollbackOf != null ? ` to v${v.rollbackOf}` : ''}${who ? ` by ${who}` : ''}`;
    case 'create':
      return `created${who ? ` by ${who}` : ''}`;
  }
}

/** Versions e rollback (H5, mockup data-screen="versions"): storico e conferma del rollback. */
export function AppVersionsPage({ repo, app, perms, onChange }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [version, setVersion] = useState(0);
  return <VersionsLoaded key={version} repo={repo} app={app} perms={perms} onChange={onChange} owner={owner} name={name} reload={() => setVersion((v) => v + 1)} />;
}

function VersionsLoaded({ repo, app, perms, onChange, owner, name, reload }: Props & { owner: string; name: string; reload: () => void }) {
  const { data, loading, error } = useLoad(() => fetchAppVersions(owner, name, app.name));
  const [target, setTarget] = useState<AppVersion | null>(null);
  const [pause, setPause] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  async function doRollback() {
    if (!target) return;
    const n = target.number;
    setTarget(null);
    setActionError(null);
    try {
      await rollbackApp(owner, name, app.name, n, pause);
      onChange(await fetchApp(owner, name, app.name));
      reload();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <>
      <AppHeader repo={repo} app={app} mode="app-versions" />
      <div className="page">
        {actionError ? <ErrorAlert message={actionError} /> : null}
        {loading && data === null ? <p className="muted">Loading versions…</p> : null}
        {error ? <ErrorAlert message={error} /> : null}
        {data ? (
          <div className="list">
            <div className="list-row list-head">
              <b style={{ color: 'var(--text)' }}>Versions</b>
              <span className="sp" />
              <span className="small">Active and previous image are always in backups</span>
            </div>
            <ul aria-label="Versions" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
              {data.items.map((v) => {
                const active = v.number === app.activeVersion;
                const failed = v.state === 'failed' || v.state === 'rolled_back';
                const sha = shortDigest(v.image.digest);
                return (
                  <li key={v.number} className="list-row">
                    <span className={`st ${failed ? 'err' : active ? 'ok' : 'skip'}`}>{failed ? <X size={12} aria-hidden="true" /> : <Check size={12} aria-hidden="true" />}</span>
                    <div style={{ flex: 1 }}>
                      <b>v{v.number}</b> {sha ? <span className="sha">{sha}</span> : null}
                      {v.trigger === 'config' ? <> <span className="badge">config</span></> : null}
                      {active ? <> <span className="badge badge-ok">active</span></> : null}
                      {failed ? <> <span className="badge badge-err">{v.state === 'rolled_back' ? 'failed health check · rolled back' : 'failed'}</span></> : null}
                      {active && v.image.verified ? (
                        <>
                          {' '}
                          <span className="badge badge-ok">
                            <ShieldCheck size={12} aria-hidden="true" />
                            verified
                          </span>
                        </>
                      ) : null}
                      <div className="small muted">
                        {describeTrigger(v, app.follow?.branch)} · {timeAgo(v.createdAt)}
                      </div>
                    </div>
                    {v.inBackup && !failed ? <span className="badge badge-info">in backup</span> : null}
                    {!v.imageAvailable && !active ? <span className="small subtle">image removed by registry cleanup</span> : null}
                    {!active && !failed && perms.canRollback ? (
                      <Button size="sm" disabled={!v.imageAvailable} aria-label={`Roll back to v${v.number}`} onClick={() => { setPause(false); setTarget(v); }}>
                        <Undo2 size={14} aria-hidden="true" />
                        Roll back
                      </Button>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null}
      </div>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(o) => {
          if (!o) setTarget(null);
        }}
        title={`Roll back ${app.name} to v${target?.number ?? ''}?`}
        confirmLabel="Roll back"
        description={
          <span className="stack" style={{ gap: 12 }}>
            <span>
              GitStack will run image <span className="sha">{shortDigest(target?.image.digest) ?? target?.image.reference}</span> with the configuration of v{target?.number} as a new version. Secrets keep their current values.
            </span>
            {app.follow && !app.follow.paused ? (
              <label className="row">
                <input type="checkbox" checked={pause} onChange={(e) => setPause(e.target.checked)} />
                <span>
                  Pause automatic deploys from <span className="mono">{app.follow.tagPattern ?? app.follow.branch}</span> (otherwise the next CI image replaces it)
                </span>
              </label>
            ) : null}
          </span>
        }
        onConfirm={() => void doRollback()}
      />
    </>
  );
}
