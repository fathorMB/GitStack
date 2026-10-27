import { Activity, Cpu, ExternalLink, RefreshCw, Rocket, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../../../components';
import { deployApp, fetchApp, fetchAppMetrics, restartApp, scaleApp } from '../../../lib/appsApi';
import type { App, AppMetrics } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { formatCpu, formatMemory, percent, shortDigest } from '../../../lib/appsView';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { AppHeader } from './AppHeader';
import { appsPath } from './appMode';

interface Props {
  repo: Repository;
  app: App;
  perms: AppPermissions;
  onChange: (app: App) => void;
}

/** Barre dello sparkline: altezze 0-100 dai punti, normalizzate sul massimo. */
function sparkHeights(points: { value: number }[]): number[] {
  const max = Math.max(...points.map((p) => p.value), 0);
  return points.map((p) => (max > 0 ? Math.max(3, Math.round((p.value / max) * 100)) : 3));
}

/** Overview (H3, mockup data-screen="overview"): varianti «in esecuzione» e «deploy fallito con ritorno automatico». */
export function AppOverviewPage({ repo, app, perms, onChange }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [metrics, setMetrics] = useState<AppMetrics | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [deployOpen, setDeployOpen] = useState(false);
  const [image, setImage] = useState('');

  useEffect(() => {
    let live = true;
    fetchAppMetrics(owner, name, app.name).then(
      (m) => live && setMetrics(m),
      () => live && setMetrics(null),
    );
    return () => {
      live = false;
    };
  }, [owner, name, app.name, app.activeVersion]);

  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      onChange(await fetchApp(owner, name, app.name));
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const failed = app.lastDeploy && (app.lastDeploy.state === 'failed' || app.lastDeploy.state === 'rolled_back') && app.runningPreviousVersion;
  const usage = metrics?.usage ?? app.usage ?? null;
  const g = app.resources.guaranteed;
  const l = app.resources.limit;
  const sha = shortDigest(app.image.digest);
  const reqPoints = metrics?.available ? (metrics.requests?.points ?? []) : [];

  const actions = (
    <>
      {app.address && app.status !== 'stopped' ? (
        <a className="btn" href={`https://${app.address}`} target="_blank" rel="noreferrer">
          <ExternalLink size={16} aria-hidden="true" />
          Open
        </a>
      ) : null}
      {perms.canRestart ? (
        <Button disabled={busy} onClick={() => void run(() => restartApp(owner, name, app.name))}>
          <RefreshCw size={16} aria-hidden="true" />
          Restart
        </Button>
      ) : null}
      {perms.canDeploy ? (
        <Button variant="primary" onClick={() => setDeployOpen((v) => !v)} aria-expanded={deployOpen}>
          <Rocket size={16} aria-hidden="true" />
          Deploy
        </Button>
      ) : null}
    </>
  );

  return (
    <>
      <AppHeader repo={repo} app={app} mode="app-overview" actions={actions} />
      <div className="page">
        {error ? (
          <div className="alert alert-danger" role="alert" style={{ marginBottom: 16 }}>
            <X size={16} aria-hidden="true" />
            <div>{error}</div>
          </div>
        ) : null}
        {deployOpen && perms.canDeploy ? (
          <form
            className="card card-b row"
            style={{ marginBottom: 16 }}
            aria-label="Deploy an image"
            onSubmit={(e) => {
              e.preventDefault();
              void run(async () => {
                await deployApp(owner, name, app.name, image.trim());
                setDeployOpen(false);
                setImage('');
              });
            }}
          >
            <input className="input mono" style={{ flex: 1 }} aria-label="Image to deploy" placeholder={app.image.reference} value={image} onChange={(e) => setImage(e.target.value)} />
            <Button type="submit" variant="primary" disabled={busy || !image.trim()}>
              Deploy image
            </Button>
          </form>
        ) : null}
        {failed && app.lastDeploy ? (
          <div className="alert alert-danger" style={{ marginBottom: 16 }}>
            <X size={16} aria-hidden="true" />
            <div>
              <b>Deploy of v{app.lastDeploy.version} failed its health check</b>
              {app.lastDeploy.reason ? <> ({app.lastDeploy.reason})</> : null}. v{app.activeVersion} is still serving traffic.{' '}
              <Link to={appsPath(owner, name, app.name, 'logs')}>See logs</Link> · <Link to={appsPath(owner, name, app.name, 'versions')}>Versions</Link>
            </div>
          </div>
        ) : null}

        <div className="grid4" style={{ marginBottom: 16 }}>
          <div className="card stat">
            <b>
              v{app.activeVersion}
              {failed && app.lastDeploy ? <span className="small muted" style={{ fontWeight: 400 }}> (v{app.lastDeploy.version} failed)</span> : null}
            </b>
            <span>
              Active version{sha ? <> · <span className="mono">{sha}</span></> : null}
            </span>
          </div>
          <div className="card stat">
            <b>
              {app.readyCopies} / {app.copies}
            </b>
            <span>Copies healthy</span>
          </div>
          <div className="card stat">
            <b>{usage?.requestsPerSecond != null ? `${Math.round(usage.requestsPerSecond * 10) / 10} req/s` : '—'}</b>
            <span>Requests · last hour</span>
          </div>
          <div className="card stat">
            <b style={failed ? { color: 'var(--danger)' } : undefined}>{usage?.errorRatio != null ? `${Math.round(usage.errorRatio * 1000) / 10}%` : '—'}</b>
            <span>Errors (5xx)</span>
          </div>
        </div>

        <div className="settings-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
          <div className="card">
            <div className="card-h">
              <Cpu size={16} aria-hidden="true" />
              Resources
              <span className="sp" />
              <span className="small muted" style={{ fontWeight: 400 }}>bar = use · line = guaranteed · end = limit</span>
            </div>
            <div className="card-b stack" style={{ gap: 14 }}>
              <div>
                <div className="row small">
                  <b>CPU</b>
                  <span className="muted">guaranteed {formatCpu(g.cpuMillis)} · limit {formatCpu(l.cpuMillis)} vCPU per copy</span>
                  <span className="sp" />
                  {usage ? `${formatCpu(usage.cpuMillis)} vCPU` : '—'}
                </div>
                <div className="meter" role="img" aria-label="CPU use">
                  <i style={{ width: `${percent(usage?.cpuMillis ?? 0, l.cpuMillis)}%` }} />
                  <b style={{ left: `${percent(g.cpuMillis, l.cpuMillis)}%` }} />
                </div>
                <span className="hint">Above the guaranteed share: slowed first if the server gets busy.</span>
              </div>
              <div>
                <div className="row small">
                  <b>Memory</b>
                  <span className="muted">guaranteed {formatMemory(g.memoryBytes)} · limit {formatMemory(l.memoryBytes)}</span>
                  <span className="sp" />
                  {usage ? formatMemory(usage.memoryBytes) : '—'}
                </div>
                <div className="meter" role="img" aria-label="Memory use">
                  <i style={{ width: `${percent(usage?.memoryBytes ?? 0, l.memoryBytes)}%` }} />
                  <b style={{ left: `${percent(g.memoryBytes, l.memoryBytes)}%` }} />
                </div>
              </div>
              <div className="row small">
                <b>Copies</b>
                {perms.canScale ? (
                  <span className="stepper">
                    <button type="button" aria-label="Fewer copies" disabled={busy || app.copies <= 1} onClick={() => void run(() => scaleApp(owner, name, app.name, app.copies - 1))}>
                      −
                    </button>
                    <span>{app.copies}</span>
                    <button type="button" aria-label="More copies" disabled={busy} onClick={() => void run(() => scaleApp(owner, name, app.name, app.copies + 1))}>
                      +
                    </button>
                  </span>
                ) : (
                  <span>{app.copies}</span>
                )}
                <span className="hint">Manual. With 2+ copies updates have no downtime.</span>
              </div>
            </div>
          </div>
          <div className="card">
            <div className="card-h">
              <Activity size={16} aria-hidden="true" />
              Requests · last 6 h
            </div>
            <div className="card-b">
              {reqPoints.length > 0 ? (
                <div className={failed ? 'spark err' : 'spark'} role="img" aria-label="Requests over the last 6 hours">
                  {sparkHeights(reqPoints).map((h, i) => (
                    <i key={i} style={{ height: `${h}%` }} />
                  ))}
                </div>
              ) : (
                <p className="muted">{metrics && !metrics.available ? 'Metrics are not available on this server.' : 'No request data yet.'}</p>
              )}
              <dl className="kv" style={{ marginTop: 14, gridTemplateColumns: '140px 1fr' }}>
                <dt>Health check</dt>
                <dd>
                  {app.healthCheck ? (
                    <>
                      <span className="mono">GET {app.healthCheck.path}</span> · every {app.healthCheck.intervalSeconds} s
                    </>
                  ) : (
                    'none'
                  )}
                </dd>
                <dt>Restarts (24 h)</dt>
                <dd>{metrics?.restarts24h ?? usage?.restarts24h ?? 0}</dd>
                <dt>Deploys</dt>
                <dd>
                  {app.follow ? (
                    <>
                      follow {app.follow.tagPattern ? 'tags ' : ''}<span className="mono">{app.follow.tagPattern ?? app.follow.branch}</span>
                      {app.follow.paused ? ' (paused)' : ' · only images signed by this repo’s CI'}
                    </>
                  ) : (
                    'manual only'
                  )}
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
