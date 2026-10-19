import { Download, RefreshCw, Square } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Button, ConfirmDialog, ErrorAlert } from '../../components';
import { artifactUrl, cancelRun, fetchArtifacts, fetchJobLog, fetchRun, fetchWorkflows, rerunRun } from '../../lib/actionsApi';
import type { Artifact, WorkflowJob, WorkflowRun } from '../../lib/actionsApi';
import { formatDuration, isActive, logLineClass, runState, shortRef } from '../../lib/actionsView';
import { formatBytes } from '../../lib/format';
import { describeError } from '../../lib/http';
import { fetchCommitStatus } from '../../lib/pullsApi';
import type { CombinedCommitStatus } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { useCanWrite } from '../../lib/useCanWrite';
import { CommitChecks } from './CommitChecks';
import { RunDot } from './RunStatus';

/** Millisecondi fra due letture di esecuzione e log finché sono in corso. */
const POLL_MS = 2000;
/** Tetto del backoff dopo letture fallite. */
const MAX_BACKOFF_MS = 30000;

/** Dettaglio di un'esecuzione (CI, mockup B2): job, log in diretta mascherati, artefatti, stati del commit, Riesegui e Annulla. */
export function RunDetailPage({ repo, pollMs = POLL_MS }: { repo: Repository; pollMs?: number }) {
  const { runId = '' } = useParams();
  return <RunDetail key={runId} repo={repo} runId={runId} pollMs={pollMs} />;
}

function RunDetail({ repo, runId, pollMs }: { repo: Repository; runId: string; pollMs: number }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const canWrite = useCanWrite(repo);
  const navigate = useNavigate();
  const [run, setRun] = useState<WorkflowRun | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [workflowName, setWorkflowName] = useState<string | null>(null);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [statuses, setStatuses] = useState<CombinedCommitStatus | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retrying, setRetrying] = useState(false);

  const active = run ? isActive(run.status) : true;

  // Esecuzione: si rilegge finché è in coda o in corso, senza ricaricare la pagina.
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let known = false;
    const tick = async () => {
      try {
        const r = await fetchRun(owner, name, runId);
        if (!live) return;
        failures = 0;
        known = true;
        setRun(r);
        setError(null);
        setRetrying(false);
        if (isActive(r.status)) timer = setTimeout(() => void tick(), pollMs);
      } catch (err) {
        if (!live) return;
        setError(describeError(err));
        // Prima lettura fallita: errore bloccante. Dopo, avviso e nuovo tentativo con backoff.
        if (!known) return;
        failures += 1;
        setRetrying(true);
        timer = setTimeout(() => void tick(), Math.min(pollMs * 2 ** failures, MAX_BACKOFF_MS));
      }
    };
    void tick();
    return () => {
      live = false;
      if (timer) clearTimeout(timer);
    };
  }, [owner, name, runId, pollMs]);

  // Contorno: nome del workflow, artefatti e stati del commit. Un errore li nasconde, non rompe la pagina.
  const workflowId = run?.workflowId;
  const sha = run?.sha;
  const finished = run ? !isActive(run.status) : false;
  useEffect(() => {
    if (!workflowId) return;
    let live = true;
    fetchWorkflows(owner, name).then(
      (ws) => live && setWorkflowName(ws.find((w) => w.id === workflowId)?.name ?? null),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [owner, name, workflowId]);
  useEffect(() => {
    if (!sha) return;
    let live = true;
    fetchCommitStatus(owner, name, sha).then(
      (s) => live && setStatuses(s),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [owner, name, sha, finished]);
  // Artefatti: una lettura all'apertura (run caricato) e una al passaggio a stato finale, non a ogni giro.
  const loaded = run !== null;
  useEffect(() => {
    if (!loaded) return;
    let live = true;
    fetchArtifacts(owner, name, runId).then(
      (a) => live && setArtifacts(a),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [owner, name, runId, loaded, finished]);

  const doRerun = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const created = await rerunRun(owner, name, runId);
      void navigate(`/${owner}/${name}/actions/runs/${created.id}`);
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  // Chiamata solo dopo la conferma del dialogo (G8, come `gs run cancel`).
  const doCancel = async () => {
    setBusy(true);
    setActionError(null);
    try {
      setRun(await cancelRun(owner, name, runId));
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  if (error && !run) return <ErrorAlert message={error} />;
  if (!run) return <p className="muted">Loading run…</p>;

  const state = runState(run.status, run.conclusion);
  const job = run.jobs.find((j) => j.id === jobId) ?? run.jobs.find((j) => runState(j.status, j.conclusion).tone === 'err') ?? run.jobs.find((j) => j.status === 'in_progress') ?? run.jobs[0] ?? null;
  const title = workflowName ?? run.workflowId;
  const duration = formatDuration(run.startedAt, run.completedAt);

  return (
    <div className="stack" style={{ gap: 6 }}>
      <div className="row" style={{ marginBottom: 6 }}>
        <RunDot status={run.status} conclusion={run.conclusion} />
        <h2 style={{ fontSize: 20, fontWeight: 600 }}>{title}</h2>
        <span className="muted" style={{ fontSize: 20 }}>
          {state.label}
        </span>
        <span className="sp" />
        {canWrite && !active ? (
          <Button onClick={() => void doRerun()} disabled={busy}>
            <RefreshCw size={16} aria-hidden="true" />
            Re-run
          </Button>
        ) : null}
        {canWrite && active ? (
          <Button variant="danger" onClick={() => setConfirming(true)} disabled={busy}>
            <Square size={16} aria-hidden="true" />
            Cancel run
          </Button>
        ) : null}
      </div>
      <p className="small muted" style={{ marginBottom: 12 }}>
        <span className="mono">{run.event}</span> on <span className="mono">{shortRef(run.ref)}</span> · commit{' '}
        <Link className="sha" to={`/${owner}/${name}/commit/${run.sha}`}>
          {run.sha.slice(0, 7)}
        </Link>
        {duration ? ` · ${duration}` : ''} · <Link to={`/${owner}/${name}/actions`}>All runs</Link>
      </p>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {retrying ? (
        <p className="small muted" role="status">
          Aggiornamento interrotto, nuovo tentativo…
        </p>
      ) : null}
      <div className="two">
        <div className="stack" style={{ gap: 12 }}>
          <nav className="jobs" aria-label="Jobs">
            {run.jobs.map((j) => (
              <a
                key={j.id}
                href={`#${j.id}`}
                className={job?.id === j.id ? 'active' : undefined}
                aria-current={job?.id === j.id ? 'true' : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  setJobId(j.id);
                }}
              >
                <RunDot status={j.status} conclusion={j.conclusion} />
                {j.name}
                <span className="sp" />
                <span className="subtle small">{jobTime(j)}</span>
              </a>
            ))}
          </nav>
          <section className="card" aria-label="Artifacts">
            <div className="card-h small">Artifacts</div>
            <div className="card-b stack small" style={{ gap: 6 }}>
              {artifacts.length === 0 ? <span className="subtle">No artifacts</span> : null}
              {artifacts.map((a) => (
                <a key={a.id} href={artifactUrl(owner, name, a.id)}>
                  <Download size={14} aria-hidden="true" /> {a.name} · {formatBytes(a.sizeBytes)}
                </a>
              ))}
              {artifacts.length > 0 ? <span className="subtle">Kept until {new Date(artifacts[0]!.expiresAt).toLocaleDateString('en-GB')}</span> : null}
            </div>
          </section>
          {statuses ? <CommitChecks status={statuses} /> : null}
        </div>
        {job ? <JobPanel owner={owner} repo={name} job={job} pollMs={pollMs} /> : <p className="muted">This run has no jobs yet.</p>}
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Cancel this run?"
        description="The jobs that are running stop now and cannot be resumed. You can re-run the workflow afterwards."
        confirmLabel="Cancel run"
        cancelLabel="Go back"
        destructive
        onConfirm={() => void doCancel()}
      />
    </div>
  );
}

function jobTime(j: WorkflowJob): string {
  const s = runState(j.status, j.conclusion);
  if (s.tone === 'skip' || s.tone === 'wait') return s.label.toLowerCase();
  return formatDuration(j.startedAt, j.completedAt);
}

/** Pannello del job: runner e log. Il log si rilegge finché il job è in corso (log in diretta). */
function JobPanel({ owner, repo, job, pollMs }: { owner: string; repo: string; job: WorkflowJob; pollMs: number }) {
  const [log, setLog] = useState<string | null>(null);
  const [logError, setLogError] = useState<string | null>(null);
  const jobActive = isActive(job.status);

  useEffect(() => {
    setLog(null);
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const text = await fetchJobLog(owner, repo, job.id);
        if (!live) return;
        setLog(text);
        setLogError(null);
      } catch (err) {
        if (!live) return;
        setLogError(describeError(err));
      }
      if (live && jobActive) timer = setTimeout(() => void tick(), pollMs);
    };
    void tick();
    return () => {
      live = false;
      if (timer) clearTimeout(timer);
    };
  }, [owner, repo, job.id, jobActive, pollMs]);

  const lines = log === null ? [] : log.replace(/\n$/, '').split('\n');
  const runner = job.runnerType === 'internal_pod' ? 'built-in runner' : (job.runnerName ?? 'external runner');
  return (
    <section className="card" aria-label={`Job ${job.name}`}>
      <div className="card-h">
        {job.name}
        <span className="sp" />
        <span className="small muted" style={{ fontWeight: 400 }}>
          {runner}
        </span>
      </div>
      {logError ? (
        <div className="card-b">
          <ErrorAlert message={logError} />
        </div>
      ) : null}
      <div className="log" role="log" aria-label={`Log of ${job.name}`} aria-live={jobActive ? 'polite' : 'off'}>
        {log === null && !logError ? <div>Loading log…</div> : null}
        {log !== null && lines.length === 0 ? <div>No output yet.</div> : null}
        {lines.map((line, i) => (
          <div key={i} className={logLineClass(line) || undefined}>
            <span className="n">{i + 1}</span>
            {line}
          </div>
        ))}
        {jobActive ? <div aria-hidden="true">▍</div> : null}
      </div>
    </section>
  );
}
