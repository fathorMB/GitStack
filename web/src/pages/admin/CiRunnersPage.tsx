import { Cpu, Plus, Server } from 'lucide-react';
import { useState } from 'react';
import type { ReactNode } from 'react';
import { Button, ConfirmDialog, CopyButton, EmptyState, ErrorAlert, useToast } from '../../components';
import { fetchSession } from '../../lib/authApi';
import { createCiRegistrationToken, deleteCiRunner, fetchCiQueue, fetchCiRunners } from '../../lib/ciApi';
import type { CiQueue, CiRunner, CiRunnerRegistrationToken } from '../../lib/ciApi';
import { formatBytes, formatDate } from '../../lib/format';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

const STATUS_TONE: Record<string, string> = { online: 'success', busy: 'warning', offline: 'text-subtle' };

function hours(seconds: number): string {
  const h = seconds / 3600;
  return h >= 1 ? `${Math.round(h)} h` : `${Math.round(seconds / 60)} min`;
}

// Admin · CI & runners (mockup B4, regole CI2, CI5, CI7): coda e limiti dal
// contratto (GET /admin/ci/queue), runner esterni con il token di registrazione.
// Solo l'amministratore dell'installazione: il controllo vero sta nel backend
// (403), qui si evita una console che non puo' funzionare. La scheda Storage
// riporta le regole di conservazione e, quando il server li manda, attesa media
// e spazio occupato (artefatti e cache valgono 0 finche' non arriva GIT-280).
export function CiRunnersPage() {
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
          <h1>CI &amp; runners</h1>
        </div>
        <ErrorAlert message="Only installation administrators can see CI and runners." />
      </div>
    );
  }
  return <CiConsole />;
}

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="card stat">
      <b>{value}</b>
      <span>{label}</span>
    </div>
  );
}

function Dot({ tone }: { tone: string }) {
  return <span className="dot" style={{ background: `var(--${tone})` }} aria-hidden="true" />;
}

function Kv({ term, children }: { term: string; children: ReactNode }) {
  return (
    <>
      <dt>{term}</dt>
      <dd>{children}</dd>
    </>
  );
}

function CiConsole() {
  const { data, loading, error, reload } = useLoad(async () => {
    const [runners, queue] = await Promise.all([fetchCiRunners(), fetchCiQueue()]);
    return { runners, queue };
  });
  const { toast } = useToast();
  const [token, setToken] = useState<CiRunnerRegistrationToken | null>(null);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<CiRunner | null>(null);

  if (loading && data === null) return <p className="muted">Loading CI…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'CI is not available.'} />;

  const runners = data.runners.items;
  const queue: CiQueue = data.queue;
  const limits = queue.resourceLimits;
  const usage =
    queue.avgQueueWaitSeconds !== undefined ||
    queue.logBytes !== undefined ||
    queue.artifactBytes !== undefined ||
    queue.cacheBytes !== undefined;
  const online =runners.filter((r) => r.status !== 'offline').length;

  async function register() {
    setBusy(true);
    try {
      setToken(await createCiRegistrationToken());
    } catch (err) {
      toast(describeError(err), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function remove(runner: CiRunner) {
    try {
      await deleteCiRunner(runner.id);
      toast(`Runner "${runner.name}" revoked.`, 'success');
      setPending(null);
      await reload();
    } catch (err) {
      toast(describeError(err), 'error');
    }
  }

  return (
    <div>
      <div className="row page-h">
        <h1>CI &amp; runners</h1>
        <span className="sp" />
        <Button variant="primary" onClick={() => void register()} disabled={busy}>
          <Plus size={16} aria-hidden="true" />
          Register external runner
        </Button>
      </div>

      <div className="grid4" style={{ marginBottom: 20 }}>
        <Stat value={`${queue.runningJobs} / ${queue.maxConcurrentJobs}`} label="Built-in slots in use" />
        <Stat value={String(queue.queuedJobs)} label="Jobs waiting · max half of the slots per owner" />
        <Stat value={String(online)} label="External runners online" />
        <Stat
          value={`${limits.cpuCores} vCPU`}
          label={`Per job · ${formatBytes(limits.memoryBytes)} RAM · ${formatBytes(limits.ephemeralStorageBytes)} disk · ${hours(limits.maxDurationSeconds)}`}
        />
      </div>

      {token ? (
        <div className="card card-b stack" style={{ gap: 8, marginBottom: 20 }}>
          <b>Registration token</b>
          <p className="hint">
            Valid until {formatDate(token.expiresAt)}. Copy it now: it is shown only once and disappears when you reload
            the page.
          </p>
          <div className="row" style={{ gap: 8 }}>
            <code className="mono grow">{token.token}</code>
            <CopyButton value={token.token} label="Copy token" />
          </div>
        </div>
      ) : null}

      <div className="settings-grid" style={{ gridTemplateColumns: '1fr 1fr', marginBottom: 20 }}>
        <div className="card">
          <div className="card-h">
            <Cpu size={16} aria-hidden="true" />
            Built-in runner
          </div>
          <div className="card-b">
            <dl className="kv">
              <Kv term="Concurrent jobs">{queue.maxConcurrentJobs}</Kv>
              <Kv term="Per job">
                {limits.cpuCores} vCPU · {formatBytes(limits.memoryBytes)} RAM · {formatBytes(limits.ephemeralStorageBytes)} disk ·{' '}
                {hours(limits.maxDurationSeconds)}
              </Kv>
              <Kv term="Priority">
                <span className="muted">Lower than GitStack services — jobs can never stop them</span>
              </Kv>
              <Kv term="Network">
                <span className="muted">Internet or proxy only · cluster network blocked</span>
              </Kv>
              <Kv term="Actions source">
                <span className="muted">github.com or an internal mirror</span>
              </Kv>
            </dl>
          </div>
        </div>

        <div className="card">
          <div className="card-h">
            <Server size={16} aria-hidden="true" />
            External runners
          </div>
          {runners.length === 0 ? (
            <EmptyState title="No external runners" description="Register one with the button above; workflows pick it with runs-on." />
          ) : (
            <div className="list" style={{ border: 0, borderRadius: 0 }}>
              {runners.map((r) => (
                <div key={r.id} className="list-row">
                  <Dot tone={STATUS_TONE[r.status] ?? 'text-subtle'} />
                  <span className="mono grow">{r.name}</span>
                  {r.labels.map((l) => (
                    <span key={l} className="badge">
                      {l}
                    </span>
                  ))}
                  <span className="small muted">{r.status}</span>
                  <Button size="sm" variant="ghost" onClick={() => setPending(r)} aria-label={`Revoke ${r.name}`}>
                    Revoke
                  </Button>
                </div>
              ))}
            </div>
          )}
          <div className="card-b small muted">
            Workflows pick them with <span className="mono">runs-on: [self-hosted, large]</span>.
          </div>
        </div>

        <div className="card" style={{ gridColumn: '1 / -1' }}>
          <div className="card-h">Storage</div>
          <div className="card-b">
            <dl className="kv">
              <Kv term="Logs">
                <span className="muted">kept 90 days · in backups</span>
              </Kv>
              <Kv term="Artifacts">
                <span className="muted">kept 30 days, up to 2 GB each · not in backups</span>
              </Kv>
              <Kv term="Caches">
                <span className="muted">10 GB per repo · not in backups</span>
              </Kv>
              <Kv term="Registry">
                <span className="muted">not in backups by default · images used by apps always included (v3)</span>
              </Kv>
            </dl>
            {usage ? (
              <dl className="kv" style={{ marginTop: 12 }}>
                {queue.avgQueueWaitSeconds !== undefined ? (
                  <Kv term="Average queue wait">{queue.avgQueueWaitSeconds} s</Kv>
                ) : null}
                {queue.logBytes !== undefined ? <Kv term="Space used by logs">{formatBytes(queue.logBytes)}</Kv> : null}
                {queue.artifactBytes !== undefined ? (
                  <Kv term="Space used by artifacts">{formatBytes(queue.artifactBytes)}</Kv>
                ) : null}
                {queue.cacheBytes !== undefined ? (
                  <Kv term="Space used by caches">{formatBytes(queue.cacheBytes)}</Kv>
                ) : null}
              </dl>
            ) : (
              <p className="hint" style={{ marginTop: 12 }}>
                Space used is not reported by the server, so no figures are shown here.
              </p>
            )}
          </div>
        </div>
      </div>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null);
        }}
        title="Revoke runner"
        description={pending ? `Runner "${pending.name}" will no longer take jobs. Its registration token stops working.` : ''}
        confirmLabel="Revoke"
        destructive
        onConfirm={() => {
          if (pending) void remove(pending);
        }}
      />
    </div>
  );
}
