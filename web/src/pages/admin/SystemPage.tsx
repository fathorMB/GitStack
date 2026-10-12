import { AlertTriangle, Check, Database, Download, Globe, Info, Lock } from 'lucide-react';
import type { ReactNode } from 'react';
import { ErrorAlert } from '../../components';
import { fetchSession } from '../../lib/authApi';
import { formatDate } from '../../lib/format';
import { fetchSystem } from '../../lib/systemApi';
import type { AdminSystem } from '../../lib/systemApi';
import { useLoad } from '../../lib/useLoad';

// Admin · System (mockup 16): database (N8), rete aziendale (N9), backup locale
// e S3 (N7), versioni di GitStack e di k3s (N10). I dati dell'host li
// pubblica `gitstack` e li legge core: la pagina non li inventa mai. Solo
// l'amministratore (403 dal backend; qui si evita una pagina che non puo'
// funzionare).
export function SystemPage() {
  const { data: session, loading, error } = useLoad(fetchSession);
  if (session === null) {
    return (
      <div>
        {error ? <ErrorAlert message={error} /> : null}
        {loading ? <p className="muted">Loading…</p> : null}
      </div>
    );
  }
  if (session.user.isAdmin !== true) {
    return (
      <div>
        <div className="page-h">
          <h1>System</h1>
        </div>
        <ErrorAlert message="Only installation administrators can see the system status." />
      </div>
    );
  }
  return <SystemConsole />;
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="list-row small">
      <b className="sys-label">{label}</b>
      {children}
    </div>
  );
}

function Dot({ tone }: { tone: 'success' | 'warning' | 'danger' | 'muted' }) {
  return <span className="dot" style={{ background: `var(--${tone === 'muted' ? 'text-muted' : tone})` }} aria-hidden="true" />;
}

function SystemConsole() {
  const { data, loading, error } = useLoad(fetchSystem);
  if (data === null) {
    return (
      <div>
        <div className="page-h">
          <h1>System</h1>
        </div>
        {error ? <ErrorAlert message={error} /> : null}
        {loading ? <p className="muted">Loading system status…</p> : null}
      </div>
    );
  }
  return <SystemView sys={data} />;
}

export function SystemView({ sys }: { sys: AdminSystem }) {
  const v = sys.versions;
  return (
    <div>
      <div className="row page-h">
        <h1>System</h1>
        {sys.published ? (
          <span className="badge badge-accent">
            <Check size={13} aria-hidden="true" />
            Host information published
          </span>
        ) : (
          <span className="badge badge-danger">
            <AlertTriangle size={13} aria-hidden="true" />
            Host information not published
          </span>
        )}
        <span className="sp" />
        <span className="muted small">
          GitStack <b className="mono">{v.gitstack || 'unknown'}</b>
          {v.k3s ? (
            <>
              {' '}
              · k3s <span className="mono">{v.k3s}</span>
            </>
          ) : null}
        </span>
      </div>

      {!sys.published ? (
        <div className="alert alert-warning section-gap" role="status">
          <AlertTriangle size={16} aria-hidden="true" />
          <div className="grow">
            The host has not published its information yet. Run <span className="mono">sudo gitstack system-info publish</span> on
            the server (the installer, <span className="mono">gitstack upgrade</span> and <span className="mono">gitstack backup</span>{' '}
            do it by themselves).
          </div>
        </div>
      ) : null}

      <K3sNotice versions={v} />

      <div className="stack" style={{ gap: 24 }}>
        <DatabaseCard sys={sys} />
        <NetworkCard sys={sys} />
        <BackupCard sys={sys} />
        {sys.updatedAt ? <p className="muted small">Published by gitstack on {formatDate(sys.updatedAt)}.</p> : null}
      </div>
    </div>
  );
}

function K3sNotice({ versions: v }: { versions: AdminSystem['versions'] }) {
  if (v.k3sUpdate === 'patch') {
    return (
      <div className="alert alert-info section-gap">
        <Info size={16} aria-hidden="true" />
        <div className="grow">
          <b>k3s patch update available:</b> <span className="mono">{v.k3s}</span> → <span className="mono">{v.k3sDeclared}</span>.
          Patch updates are applied automatically by <span className="mono">gitstack upgrade</span>, with a short announced downtime
          and a backup first.
        </div>
      </div>
    );
  }
  if (v.k3sUpdate === 'minor') {
    return (
      <div className="alert alert-info section-gap">
        <Info size={16} aria-hidden="true" />
        <div className="grow">
          <b>k3s minor update available:</b> <span className="mono">{v.k3s}</span> → <span className="mono">{v.k3sDeclared}</span>.
          Minor k3s upgrades are never automatic: run <span className="mono">gitstack upgrade --k3s</span> (one minor at a time,
          backup first).
        </div>
      </div>
    );
  }
  if (v.k3s) {
    return (
      <p className="muted small section-gap">
        k3s <span className="mono">{v.k3s}</span> is up to date
        {v.k3sDeclared ? (
          <>
            {' '}
            (declared <span className="mono">{v.k3sDeclared}</span>)
          </>
        ) : null}
        .
      </p>
    );
  }
  return null;
}

function DatabaseCard({ sys }: { sys: AdminSystem }) {
  const d = sys.database;
  const mode = d.mode === 'external' ? 'External' : d.mode === 'internal' ? 'Internal (bundled)' : 'Unknown';
  return (
    <div className="card">
      <div className="card-h">
        <Database size={16} aria-hidden="true" />
        Database
        <span className="sp" />
        <span className="badge">{mode}</span>
      </div>
      <div className="list list-clean">
        <Row label="PostgreSQL">
          {d.serverVersion ? (
            <>
              <span className="mono">{d.serverVersion}</span>
              <span className="muted">{d.versionLive ? 'read now from the core connection' : 'detected at install time'}</span>
            </>
          ) : (
            <span className="muted">version unknown</span>
          )}
        </Row>
        {d.mode === 'external' ? (
          <>
            <Row label="Host">
              <span className="mono">{d.port ? `${d.host}:${d.port}` : d.host}</span>
            </Row>
            <Row label="Database">
              <span className="mono">{d.name}</span>
            </Row>
            <Row label="TLS (sslmode)">
              <span className="mono">{d.sslmode || 'not set'}</span>
              {d.sslmode ? <Lock size={13} aria-hidden="true" /> : null}
            </Row>
          </>
        ) : null}
      </div>
    </div>
  );
}

function NetworkCard({ sys }: { sys: AdminSystem }) {
  const n = sys.network;
  return (
    <div className="card">
      <div className="card-h">
        <Globe size={16} aria-hidden="true" />
        Network
      </div>
      {!n.configured ? (
        <div className="card-b small muted">Not configured: no proxy and no company CA.</div>
      ) : (
        <div className="list list-clean">
          <Row label="Proxy">
            {n.httpProxy || n.httpsProxy ? (
              <>
                <span className="mono">{n.httpProxy || n.httpsProxy}</span>
                {n.httpProxy && n.httpsProxy && n.httpProxy !== n.httpsProxy ? (
                  <span className="muted">
                    HTTPS: <span className="mono">{n.httpsProxy}</span>
                  </span>
                ) : null}
                {n.noProxy ? (
                  <span className="muted">
                    no proxy: <span className="mono">{n.noProxy}</span>
                  </span>
                ) : null}
                {n.proxyHasCredentials ? <span className="badge">credentials set (not shown)</span> : null}
              </>
            ) : (
              <span className="muted">Not configured</span>
            )}
          </Row>
          <Row label="Company CA">
            <span>{n.corporateCA ? 'Installed in GitStack services' : <span className="muted">Not configured</span>}</span>
          </Row>
          <Row label="Mirror CA">
            <span>{n.mirrorCA ? 'Private CA for push mirrors' : <span className="muted">Not configured</span>}</span>
          </Row>
        </div>
      )}
    </div>
  );
}

function runText(run: AdminSystem['backup']['last']): string {
  return run.at ? formatDate(run.at) : '';
}

function BackupCard({ sys }: { sys: AdminSystem }) {
  const b = sys.backup;
  const s3 = b.s3;
  const last = b.last;
  return (
    <div className="card">
      <div className="card-h">
        <Download size={16} aria-hidden="true" />
        Backups
      </div>
      <div className="list list-clean">
        <Row label="Local">
          {b.destination ? (
            <>
              <span className="mono">{b.destination}</span>
              {b.retention ? <span className="muted">keep last {b.retention}</span> : null}
            </>
          ) : (
            <span className="muted">Not configured</span>
          )}
        </Row>
        <Row label="Last backup">
          {last.state === 'success' ? (
            <>
              <Dot tone="success" />
              <span>{runText(last)}</span>
              {last.name ? <span className="mono muted">{last.name}</span> : null}
            </>
          ) : last.state === 'failed' ? (
            <>
              <Dot tone="danger" />
              <span>Backup failed {runText(last)}</span>
              {last.error ? <span className="muted">{last.error}</span> : null}
            </>
          ) : (
            <span className="muted">No backup yet</span>
          )}
        </Row>
        <Row label="Copy to S3">
          {!s3.configured ? (
            <span className="muted">Not configured</span>
          ) : (
            <>
              <span className="mono">
                s3://{s3.bucket}
                {s3.prefix ? `/${s3.prefix}` : ''}
              </span>
              {s3.endpoint ? <span className="muted">{s3.endpoint}</span> : null}
              <span className="muted">encrypted{s3.retention ? ` · keep ${s3.retention}` : ''}</span>
            </>
          )}
        </Row>
        {s3.configured ? (
          <Row label="Last upload">
            {s3.upload.state === 'success' ? (
              <>
                <Dot tone="success" />
                <span>{runText(s3.upload)}</span>
                {s3.upload.name ? <span className="mono muted">{s3.upload.name}</span> : null}
              </>
            ) : s3.upload.state === 'failed' ? (
              <>
                <Dot tone="warning" />
                <span>S3 upload failed {runText(s3.upload)}</span>
                <span className="muted">
                  {s3.upload.error ? `${s3.upload.error} · ` : ''}the local backup is valid
                </span>
              </>
            ) : (
              <span className="muted">No upload yet</span>
            )}
          </Row>
        ) : null}
      </div>
    </div>
  );
}
