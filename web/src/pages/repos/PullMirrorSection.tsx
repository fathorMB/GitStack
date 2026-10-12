import { AlertCircle, Check, CheckCircle, Clock, GitBranch, Key, Loader2, StopCircle } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, ErrorAlert, FormField, PasswordInput, Select, useToast } from '../../components';
import { timeAgo } from '../../lib/format';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { fetchPullMirror, fetchPullMirrorRuns, pausePullMirror, resumePullMirror, stopPullMirror, syncPullMirror, updatePullMirror } from '../../lib/pullMirrorsApi';
import type { PullMirror, PullMirrorRun } from '../../lib/pullMirrorsApi';
import type { PullMirrorRunRef } from '@gitstack/api-client';
import { useLoad } from '../../lib/useLoad';

// Stato del mirror = colore + icona + testo.
const STATE: Record<PullMirror['state'], { label: string; color: string; Icon: typeof Clock }> = {
  pending: { label: 'Queued', color: 'var(--info)', Icon: Clock },
  syncing: { label: 'Syncing', color: 'var(--info)', Icon: Loader2 },
  in_sync: { label: 'Active', color: 'var(--success)', Icon: CheckCircle },
  error: { label: 'Error', color: 'var(--danger)', Icon: AlertCircle },
  paused: { label: 'Paused', color: 'var(--warning)', Icon: Clock },
  stopped: { label: 'Stopped', color: 'var(--text-subtle)', Icon: AlertCircle },
};

const RUN_OUTCOME: Record<PullMirrorRun['outcome'], { label: string; color: string }> = {
  success: { label: 'Success', color: 'var(--success)' },
  diverged: { label: 'Diverged', color: 'var(--warning)' },
  error: { label: 'Error', color: 'var(--danger)' },
  blocked: { label: 'Blocked', color: 'var(--danger)' },
};

const INTERVALS = [
  { value: '300', label: '5 minutes' },
  { value: '600', label: '10 minutes' },
  { value: '900', label: '15 minutes' },
  { value: '1800', label: '30 minutes' },
  { value: '3600', label: '1 hour' },
  { value: '7200', label: '2 hours' },
  { value: '14400', label: '4 hours' },
  { value: '28800', label: '8 hours' },
  { value: '43200', label: '12 hours' },
  { value: '86400', label: '24 hours' },
];

function shortSha(sha: string): string {
  return sha.slice(0, 7);
}

// Intervallo in secondi → etichetta leggibile.
function intervalLabel(seconds: number): string {
  if (seconds === 300) return '5 minutes';
  if (seconds === 600) return '10 minutes';
  if (seconds === 900) return '15 minutes';
  if (seconds === 1800) return '30 minutes';
  if (seconds === 3600) return '1 hour';
  if (seconds === 7200) return '2 hours';
  if (seconds === 14400) return '4 hours';
  if (seconds === 28800) return '8 hours';
  if (seconds === 43200) return '12 hours';
  if (seconds === 86400) return '24 hours';
  return `${seconds} seconds`;
}

/**
 * Sezione Pull mirror (P3, MP3, MP4, MP6).
 * Visibile solo ad admin. Gestisce stato attivo/in pausa/fermo,
 * sorgente, credenziali, intervallo, sincronizzazione, divergenze, registro.
 */
export function PullMirrorSection({
  owner,
  repo,
}: {
  owner: string;
  repo: string;
}) {
  const { toast } = useToast();
  const { data, loading, error } = useLoad(() => fetchPullMirror(owner, repo));
  const [confirm, setConfirm] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const [busy, setBusy] = useState('');

  if (loading && data === null) return <p className="muted">Loading pull mirror settings…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Pull mirror not found.'} />;

  const m = data;

  // Stato corrente del mirror per decidere cosa mostrare.
  const isStopped = m.state === 'stopped';
  const isPaused = m.state === 'paused';
  const isActive = !isStopped && !isPaused;

  async function action(fn: () => Promise<PullMirror>, label: string) {
    setBusy(label);
    setActionError('');
    try {
      await fn();
      toast(`${label} completed successfully.`, 'success');
    } catch (err) {
      if (err instanceof ApiError) {
        const fields = fieldErrors(err);
        setActionError(Object.keys(fields).length > 0 ? fields[Object.keys(fields)[0]] : err.message);
      } else {
        setActionError(describeError(err));
      }
    } finally {
      setBusy('');
    }
  }

  return (
    <section aria-label="Pull mirror">
      <div className="sec-h">
        <h2>Pull mirror</h2>
        {isActive ? (
          <span className="badge badge-ok" style={{ color: STATE[m.state].color, borderColor: STATE[m.state].color }}>
            <span className="dot" style={{ background: STATE[m.state].color }} />
            {STATE[m.state].label}
          </span>
        ) : null}
        {isPaused ? <span className="badge badge-paused">Paused</span> : null}
        <span className="sp" />
        {isActive ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void action(() => syncPullMirror(owner, repo), 'Syncing')}
            disabled={busy !== ''}
          >
            Sync now
          </Button>
        ) : null}
      </div>

      <div className="card card-b stack" style={{ gap: 16 }}>
        {actionError ? <ErrorAlert message={actionError} /> : null}
        <PullMirrorForm owner={owner} repo={repo} mirror={m} />
        <dl className="kv">
          <dt>What is copied</dt>
          <dd className="muted">All branches and tags. Never deleted or overwritten. Issues, releases, wiki and LFS files of the source are not copied.</dd>
        </dl>
        <div className="alert alert-info">
          <AlertCircle size={16} aria-hidden="true" />
          <div>
            If a successful update doesn&apos;t happen for <b>24 hours</b>, repository admins get a notification.
          </div>
        </div>
      </div>

      {/* Diverged refs */}
      <DivergedRefsSection divergedRefs={m.divergedRefs} />

      {/* Recent runs */}
      <RecentRunsSection owner={owner} repo={repo} />

      {/* Danger zone */}
      <DangerZone owner={owner} repo={repo} mirror={m} isPaused={isPaused} isStopped={isStopped} onAction={action} setConfirm={setConfirm} busy={busy} />

      {/* Confirm dialog per stop */}
      <ConfirmDialog
        open={confirm === 'stop'}
        onOpenChange={(open: boolean) => !open && setConfirm(null)}
        title={`Stop mirroring ${repo}?`}
        description="The repository becomes a normal, writable repository. Source credentials are deleted; run history and diverged refs stay."
        confirmLabel={repo}
        destructive
        onConfirm={() => void action(() => stopPullMirror(owner, repo, repo), 'Stopping')}
      />
    </section>
  );
}

/**
 * Form inline per modificare intervallo e credenziali del pull mirror.
 */
function PullMirrorForm({
  owner,
  repo,
  mirror,
}: {
  owner: string;
  repo: string;
  mirror: PullMirror;
}) {
  const { toast } = useToast();
  const [interval, setInterval] = useState(String(mirror.intervalSeconds));
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErrors({});
    try {
      const input: { intervalSeconds?: number; token?: string } = {};
      const newInterval = Number(interval);
      if (newInterval !== mirror.intervalSeconds && newInterval >= 300 && newInterval <= 86400) {
        input.intervalSeconds = newInterval;
      }
      if (token !== '') {
        input.token = token;
      }
      await updatePullMirror(owner, repo, input);
      toast('Pull mirror settings saved.', 'success');
    } catch (err) {
      if (err instanceof ApiError && (err.status === 400 || err.status === 409 || err.status === 422)) {
        const fields = fieldErrors(err);
        if (Object.keys(fields).length > 0) setErrors(fields);
        else setErrors({ interval: describeError(err) });
      } else {
        setErrors({ interval: describeError(err) });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="kv" onSubmit={(e: FormEvent) => void submit(e)}>
      <dt>Source</dt>
      <dd className="mono">{mirror.origin}</dd>

      <dt>Credentials</dt>
      <dd>
        <div className="row" style={{ gap: 8, alignItems: 'center' }}>
          <Key size={14} className="muted" aria-hidden="true" />
          {mirror.username ? (
            <>
              <span className="mono">{mirror.username}</span>
              <span className="subtle">· token set (hidden)</span>
            </>
          ) : (
            <span className="subtle">No credentials</span>
          )}
          <span className="sp" />
          <div className="row" style={{ gap: 8 }}>
            <FormField label="" error={errors.token}>
              <PasswordInput
                placeholder="New token"
                value={token}
                autoComplete="new-password"
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setToken(e.target.value)}
                aria-label="New token to replace"
              />
            </FormField>
            <Button type="submit" variant="ghost" size="sm" disabled={busy} onClick={() => void submit(new Event('submit') as unknown as FormEvent)}>
              Replace
            </Button>
            {mirror.hasToken ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => {
                  updatePullMirror(owner, repo, { token: '' }).then(() => {
                    toast('Token removed.', 'success');
                  }).catch((err: unknown) => {
                    if (err instanceof ApiError) {
                      setErrors({ token: err.message });
                    }
                  });
                }}
              >
                Remove
              </Button>
            ) : null}
          </div>
        </div>
      </dd>

      <dt>Update every</dt>
      <dd>
        <div className="row" style={{ gap: 8, alignItems: 'center' }}>
          <Select
            value={interval}
            onValueChange={(v: string) => setInterval(v)}
            options={INTERVALS}
            aria-label="Update interval"
          />
          <span className="hint">From 5 minutes to 24 hours.</span>
        </div>
      </dd>

      <dt>Last update</dt>
      <dd>
        {mirror.lastSyncedAt ? (
          <span>
            <Check size={14} style={{ color: 'var(--success)' }} aria-hidden="true" />{' '}
            {timeAgo(mirror.lastSyncedAt)}
            {mirror.divergedRefs.length > 0 && (
              <span className="muted"> · {mirror.divergedRefs.length} ref{mirror.divergedRefs.length > 1 ? 's' : ''} diverged</span>
            )}
          </span>
        ) : (
          <span className="subtle">No updates yet</span>
        )}
      </dd>
    </form>
  );
}

/**
 * Sezione ref diverged (P3, MP1).
 */
function DivergedRefsSection({
  divergedRefs,
}: {
  divergedRefs: PullMirror['divergedRefs'];
}) {
  if (divergedRefs.length === 0) return null;

  return (
    <section aria-label="Diverged refs">
      <div className="sec-h">
        <h2>Diverged refs</h2>
        <span className="counter">{divergedRefs.length}</span>
      </div>
      <div className="list">
        <div className="list-row list-head">
          <span style={{ width: 200 }}>Ref</span>
          <span style={{ width: 110 }}>Here</span>
          <span style={{ width: 110 }}>Source</span>
          <span className="sp">Since</span>
        </div>
        {divergedRefs.map((ref: PullMirror['divergedRefs'][number]) => (
          <div className="list-row" key={ref.ref}>
            <span className="mono" style={{ width: 200 }}>
              <GitBranch size={14} aria-hidden="true" className="icon" />
              {ref.ref}
            </span>
            <span style={{ width: 110 }} className="sha">{shortSha(ref.localSha)}</span>
            <span style={{ width: 110 }} className="sha">{ref.remoteSha ? shortSha(ref.remoteSha) : '—'}</span>
            <span className="small muted sp">{ref.detectedAt ? new Date(ref.detectedAt).toLocaleString('en-GB') : ''}</span>
          </div>
        ))}
      </div>
      <p className="hint" style={{ marginTop: 8 }}>
        GitStack keeps its version. A diverged ref is never overwritten while the mirror is active; it is left as it is if you stop the mirror.
      </p>
    </section>
  );
}

/**
 * Sezione registro esecuzioni recenti (P3).
 */
function RecentRunsSection({ owner, repo }: { owner: string; repo: string }) {
  const { data } = useLoad(() => fetchPullMirrorRuns(owner, repo));
  const runs = data ?? [];

  if (runs.length === 0) return null;

  return (
    <section aria-label="Recent runs">
      <div className="sec-h">
        <h2>Recent runs</h2>
        <span className="hint">Last {runs.length} runs, kept 30 days</span>
      </div>
      <div className="list">
        <table className="log">
          <thead>
            <tr>
              <th>When</th>
              <th>Result</th>
              <th>Details</th>
              <th>Duration</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r: PullMirrorRun) => {
              const o = RUN_OUTCOME[r.outcome];
              return (
                <tr key={r.id}>
                  <td className="muted">{timeAgo(r.startedAt)}</td>
                  <td>
                    <span className="badge" style={{ color: o.color, borderColor: o.color }}>{o.label}</span>
                  </td>
                  <td>
                    {r.refs.length > 0 ? (
                      <div className="mono small">
                        {r.refs.map((ref: PullMirrorRunRef) => (
                          <div key={ref.ref}>
                            {ref.ref} · {ref.status}
                            {ref.newSha ? ` · ${shortSha(ref.newSha)}` : ''}
                            {ref.reason ? ` · ${ref.reason}` : ''}
                          </div>
                        ))}
                      </div>
                    ) : (
                      <span className="subtle">—</span>
                    )}
                    {r.error ? <div className="small" style={{ color: 'var(--danger)' }}>{r.error}</div> : null}
                  </td>
                  <td>{r.durationMs} ms</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}

/**
 * Danger zone: Pausa, Riprendi, Interrompi (P3, MP6, G8).
 */
function DangerZone({
  owner,
  repo,
  mirror,
  isPaused,
  isStopped,
  onAction,
  setConfirm,
  busy,
}: {
  owner: string;
  repo: string;
  mirror: PullMirror;
  isPaused: boolean;
  isStopped: boolean;
  onAction: (fn: () => Promise<PullMirror>, label: string) => void;
  setConfirm: (c: string | null) => void;
  busy: string;
}) {
  return (
    <section aria-label="Danger zone">
      <div className="sec-h">
        <h2 style={{ color: 'var(--danger)' }}>Danger zone</h2>
      </div>
      <div className="list" style={{ borderColor: 'var(--danger)' }}>
        {/* Pause / Resume */}
        {!isStopped && !isPaused ? (
          <div className="list-row" style={{ padding: '14px 16px' }}>
            <div style={{ flex: 1 }}>
              <b>Pause mirror</b>
              <div className="small muted">Stop updates for now. The repository stays a read-only mirror; resume with one click.</div>
            </div>
            <Button
              variant="ghost"
              onClick={() => onAction(() => pausePullMirror(owner, repo), 'Pausing')}
              disabled={busy !== ''}
            >
              <StopCircle size={16} aria-hidden="true" />
              Pause
            </Button>
          </div>
        ) : null}

        {isPaused ? (
          <div className="list-row" style={{ padding: '14px 16px' }}>
            <div style={{ flex: 1 }}>
              <b>Resume mirror</b>
              <div className="small muted">Updates restart now, then every {intervalLabel(mirror.intervalSeconds)}.</div>
            </div>
            <Button
              variant="primary"
              onClick={() => onAction(() => resumePullMirror(owner, repo), 'Resuming')}
              disabled={busy !== ''}
            >
              Resume
            </Button>
          </div>
        ) : null}

        {/* Stop mirroring */}
        {!isStopped && !isPaused ? (
          <div className="list-row" style={{ padding: '14px 16px' }}>
            <div style={{ flex: 1 }}>
              <b>Stop mirroring</b>
              <div className="small muted">The repository becomes a normal, writable repository. Source credentials are deleted; run history and diverged refs stay. You can enable a pull mirror again later.</div>
            </div>
            <Button
              variant="danger"
              onClick={() => setConfirm('stop')}
              disabled={busy !== ''}
            >
              <StopCircle size={16} aria-hidden="true" />
              Stop mirroring
            </Button>
          </div>
        ) : null}
      </div>
    </section>
  );
}
