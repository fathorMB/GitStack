import * as Dialog from '@radix-ui/react-dialog';
import { Play, Plus } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert, Select, TextInput } from '../../components';
import { dispatchWorkflow, fetchRuns, fetchWorkflows } from '../../lib/actionsApi';
import type { Workflow, WorkflowRun } from '../../lib/actionsApi';
import { filterRuns, formatDuration, isActive, shortRef } from '../../lib/actionsView';
import { timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import type { Repository } from '../../lib/reposApi';
import { useCanWrite } from '../../lib/useCanWrite';
import { useLoad } from '../../lib/useLoad';
import { RunBadge, RunDot } from './RunStatus';

interface Loaded {
  workflows: Workflow[];
  runs: WorkflowRun[];
}

/** Scheda Actions (CI, mockup B1): workflow, esecuzioni con filtri, coda e avvio a mano (solo con write). */
export function ActionsPage({ repo }: { repo: Repository }) {
  const [version, setVersion] = useState(0);
  return <ActionsLoaded key={version} repo={repo} reload={() => setVersion((v) => v + 1)} />;
}

function ActionsLoaded({ repo, reload }: { repo: Repository; reload: () => void }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const canWrite = useCanWrite(repo);
  const { data, error } = useLoad<Loaded>(async () => {
    const [workflows, runs] = await Promise.all([fetchWorkflows(owner, name), fetchRuns(owner, name)]);
    return { workflows, runs };
  });
  const [workflowId, setWorkflowId] = useState<string | null>(null);
  const [quick, setQuick] = useState<'all' | 'failed' | 'running'>('all');
  const [text, setText] = useState('');
  const [dispatching, setDispatching] = useState(false);

  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading workflow runs…</p>;

  const wfName = (id: string) => data.workflows.find((w) => w.id === id)?.name ?? id;
  const shown = filterRuns(data.runs, { workflowId, quick, text });
  const running = data.runs.filter((r) => r.status === 'in_progress').length;
  const waiting = data.runs.filter((r) => r.status === 'queued').length;
  const target = data.workflows.find((w) => w.id === workflowId) ?? data.workflows.find((w) => w.state === 'active') ?? null;

  return (
    <div className="two">
      <div className="stack" style={{ gap: 4 }}>
        <div className="small muted" style={{ padding: '0 10px 6px', fontWeight: 600 }}>
          Workflows
        </div>
        <nav className="jobs" aria-label="Workflows">
          <a href="#all" className={workflowId === null ? 'active' : undefined} aria-current={workflowId === null ? 'true' : undefined} onClick={(e) => { e.preventDefault(); setWorkflowId(null); }}>
            All workflows
          </a>
          {data.workflows.map((w) => (
            <a key={w.id} href={`#${w.id}`} className={workflowId === w.id ? 'active' : undefined} aria-current={workflowId === w.id ? 'true' : undefined} onClick={(e) => { e.preventDefault(); setWorkflowId(w.id); }}>
              {w.name}
              <span className="subtle small mono">{w.path.replace(/\/[^/]*$/, '')}</span>
            </a>
          ))}
        </nav>
      </div>
      <div className="stack" style={{ gap: 14 }}>
        <div className="row">
          <TextInput
            aria-label="Filter runs"
            placeholder="Filter runs: branch:, event:, status:"
            value={text}
            onChange={(e) => setText(e.target.value)}
            style={{ flex: 1, maxWidth: 420 }}
          />
          <span className="seg" role="group" aria-label="Run state">
            {(['all', 'failed', 'running'] as const).map((q) => (
              <button key={q} type="button" className={quick === q ? 'active' : undefined} aria-pressed={quick === q} onClick={() => setQuick(q)}>
                {q === 'all' ? 'All' : q === 'failed' ? 'Failed' : 'Running'}
              </button>
            ))}
          </span>
          <span className="sp" />
          {canWrite ? (
            <Button onClick={() => setDispatching(true)} disabled={target === null}>
              <Play size={16} aria-hidden="true" />
              Run workflow
            </Button>
          ) : null}
        </div>
        <div className="list" role="list" aria-label="Workflow runs">
          <div className="list-row list-head">
            <b style={{ color: 'var(--text)' }}>{shown.length === data.runs.length ? `${data.runs.length} runs` : `${shown.length} of ${data.runs.length} runs`}</b>
            <span className="sp" />
            <span className="small">
              Queue: {running} running · {waiting} waiting
            </span>
          </div>
          {shown.length === 0 ? (
            <EmptyState
              icon={<Plus size={32} aria-hidden="true" />}
              title={data.runs.length === 0 ? 'No workflow runs yet' : 'No runs match the filters'}
              description={data.runs.length === 0 ? 'Add a workflow under .gitstack/workflows or .github/workflows, then push.' : 'Change the filters to see more runs.'}
            />
          ) : null}
          {shown.map((r) => (
            <RunRow key={r.id} owner={owner} repo={name} run={r} workflow={wfName(r.workflowId)} />
          ))}
        </div>
      </div>
      {dispatching && target ? (
        <DispatchDialog
          owner={owner}
          repo={name}
          workflows={data.workflows.filter((w) => w.state === 'active')}
          initial={target.id}
          defaultRef={repo.defaultBranch}
          onClose={() => setDispatching(false)}
          onDone={() => {
            setDispatching(false);
            reload();
          }}
        />
      ) : null}
    </div>
  );
}

function RunRow({ owner, repo, run: r, workflow }: { owner: string; repo: string; run: WorkflowRun; workflow: string }) {
  const duration = formatDuration(r.startedAt, r.completedAt);
  const cancelled = r.status === 'cancelled' || r.conclusion === 'cancelled';
  return (
    <div className="list-row hover" role="listitem">
      <RunDot status={r.status} conclusion={r.conclusion} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <Link to={`/${owner}/${repo}/actions/runs/${r.id}`} style={{ fontWeight: 600, color: 'var(--text)' }}>
          {workflow}
        </Link>
        <div className="small muted">
          <span className="mono">{r.event}</span> · <span className="mono">{shortRef(r.ref)}</span> · <span className="sha">{r.sha.slice(0, 7)}</span> ·{' '}
          <span title={r.createdAt}>{timeAgo(r.createdAt)}</span>
          {cancelled ? ' · cancelled' : ''}
        </div>
      </div>
      <RunBadge status={r.status} conclusion={r.conclusion} duration={isActive(r.status) || cancelled ? undefined : duration} />
    </div>
  );
}

interface InputRow {
  key: number;
  name: string;
  value: string;
}

/** Avvio a mano (mockup B1, variante «Avvio a mano»): ramo e input di workflow_dispatch. Il token del job non supera mai i permessi di chi avvia. */
function DispatchDialog({
  owner,
  repo,
  workflows,
  initial,
  defaultRef,
  onClose,
  onDone,
}: {
  owner: string;
  repo: string;
  workflows: Workflow[];
  initial: string;
  defaultRef: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const [workflowId, setWorkflowId] = useState(initial);
  const [ref, setRef] = useState(defaultRef);
  const [rows, setRows] = useState<InputRow[]>([]);
  const [next, setNext] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wf = workflows.find((w) => w.id === workflowId);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!ref.trim()) {
      setError('Choose the branch or tag to run on.');
      return;
    }
    setBusy(true);
    setError(null);
    const inputs: Record<string, string> = {};
    for (const r of rows) if (r.name.trim()) inputs[r.name.trim()] = r.value;
    try {
      await dispatchWorkflow(owner, repo, workflowId, ref.trim(), inputs);
      onDone();
    } catch (err) {
      setError(describeError(err));
      setBusy(false);
    }
  };

  return (
    <Dialog.Root open onOpenChange={(o) => (!o && !busy ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="dialog dialog-wide">
          <form onSubmit={(e) => void submit(e)} className="stack" style={{ gap: 14 }}>
            <Dialog.Title className="dialog-title">Run workflow{wf ? ` · ${wf.name}` : ''}</Dialog.Title>
            <Dialog.Description className="hint">Requires write access. The job token never has more permissions than you.</Dialog.Description>
            <div className="field">
              <label htmlFor="dispatch-workflow">Workflow</label>
              <Select
                id="dispatch-workflow"
                value={workflowId}
                onValueChange={setWorkflowId}
                options={workflows.map((w) => ({ value: w.id, label: w.name }))}
              />
            </div>
            <div className="field">
              <label htmlFor="dispatch-ref">Use workflow from</label>
              <TextInput id="dispatch-ref" className="mono" value={ref} onChange={(e) => setRef(e.target.value)} />
            </div>
            {rows.map((r) => (
              <div key={r.key} className="row">
                <TextInput
                  aria-label="Input name"
                  placeholder="name"
                  className="mono"
                  value={r.name}
                  onChange={(e) => setRows(rows.map((x) => (x.key === r.key ? { ...x, name: e.target.value } : x)))}
                />
                <TextInput
                  aria-label={`Value of ${r.name || 'input'}`}
                  placeholder="value"
                  value={r.value}
                  onChange={(e) => setRows(rows.map((x) => (x.key === r.key ? { ...x, value: e.target.value } : x)))}
                />
                <Button size="sm" onClick={() => setRows(rows.filter((x) => x.key !== r.key))} aria-label={`Remove input ${r.name || ''}`.trim()}>
                  Remove
                </Button>
              </div>
            ))}
            <div>
              <Button
                size="sm"
                onClick={() => {
                  setRows([...rows, { key: next, name: '', value: '' }]);
                  setNext(next + 1);
                }}
              >
                <Plus size={14} aria-hidden="true" />
                Add input
              </Button>
            </div>
            {error ? <ErrorAlert message={error} /> : null}
            <div className="dialog-actions">
              <Button onClick={onClose} disabled={busy}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={busy}>
                <Play size={16} aria-hidden="true" />
                Run workflow
              </Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
