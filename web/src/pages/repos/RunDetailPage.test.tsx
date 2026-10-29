import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRun } from '../../lib/actionsApi';
import type { Repository } from '../../lib/reposApi';
import { RunDetailPage } from './RunDetailPage';

vi.mock('../../lib/actionsApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/actionsApi')>()),
  fetchRun: vi.fn(),
  fetchWorkflows: vi.fn(),
  fetchJobLog: vi.fn(),
  fetchArtifacts: vi.fn(),
  cancelRun: vi.fn(),
  rerunRun: vi.fn(),
}));
vi.mock('../../lib/pullsApi', async (orig) => ({ ...(await orig<typeof import('../../lib/pullsApi')>()), fetchCommitStatus: vi.fn() }));
vi.mock('../../lib/useCanWrite', () => ({ useCanWrite: vi.fn() }));

import { cancelRun, fetchArtifacts, fetchJobLog, fetchRun, fetchWorkflows, rerunRun } from '../../lib/actionsApi';
import { fetchCommitStatus } from '../../lib/pullsApi';
import { useCanWrite } from '../../lib/useCanWrite';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', archived: false } as Repository;

const perms = { contents: 'read' } as unknown as WorkflowRun['jobs'][number]['permissions'];
const run = (over: Partial<WorkflowRun>): WorkflowRun => ({
  id: 'run1',
  workflowId: 'wf1',
  status: 'completed',
  conclusion: 'failure',
  event: 'push',
  ref: 'refs/heads/fix/webhook-retry',
  sha: 'a91c3e2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  createdAt: '2026-10-19T10:00:00Z',
  startedAt: '2026-10-19T10:00:00Z',
  completedAt: '2026-10-19T10:06:12Z',
  jobs: [
    { id: 'j1', name: 'lint', status: 'completed', conclusion: 'success', runnerType: 'internal_pod', permissions: perms, startedAt: '2026-10-19T10:00:00Z', completedAt: '2026-10-19T10:00:48Z' },
    { id: 'j2', name: 'test (go 1.23)', status: 'completed', conclusion: 'failure', runnerType: 'internal_pod', permissions: perms, startedAt: '2026-10-19T10:01:00Z', completedAt: '2026-10-19T10:06:02Z' },
  ],
  ...over,
});

function setup(r: WorkflowRun, write = true) {
  vi.mocked(useCanWrite).mockReturnValue(write);
  vi.mocked(fetchRun).mockResolvedValue(r);
  vi.mocked(fetchWorkflows).mockResolvedValue([{ id: 'wf1', path: '.github/workflows/ci.yml', name: 'ci', state: 'active' }]);
  vi.mocked(fetchArtifacts).mockResolvedValue([{ id: 'a1', name: 'test-report.xml', sizeBytes: 212 * 1024, expiresAt: '2026-11-18T00:00:00Z' }]);
  vi.mocked(fetchCommitStatus).mockRejectedValue(new Error('none'));
  vi.mocked(fetchJobLog).mockResolvedValue('Run go test\nenv: DATABASE_URL=***\n--- FAIL: TestX\nError: Process completed with exit code 1.\n');
  return render(
    <MemoryRouter initialEntries={['/acme/api/actions/runs/run1']}>
      <Routes>
        <Route path="/:owner/:repo/actions/runs/:runId" element={<RunDetailPage repo={repo} pollMs={20} />} />
        <Route path="/:owner/:repo/actions/runs/run2" element={<p>new run</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.mocked(fetchRun).mockReset();
  vi.mocked(fetchArtifacts).mockReset();
  vi.mocked(fetchJobLog).mockReset();
  vi.mocked(cancelRun).mockReset();
  vi.mocked(rerunRun).mockReset();
});

describe('RunDetailPage', () => {
  it('fallita: job, log del job fallito con segreti mascherati, artefatti e Riesegui', async () => {
    setup(run({}));
    expect(await screen.findByRole('heading', { name: 'ci' }, { timeout: 5000 })).toBeInTheDocument();
    const log = await screen.findByRole('log', { name: 'Log of test (go 1.23)' }, { timeout: 5000 });
    expect(within(log).getByText(/DATABASE_URL=\*\*\*/)).toBeInTheDocument();
    expect(within(log).getByText(/--- FAIL: TestX/).closest('div')).toHaveClass('er');
    expect(screen.getByRole('link', { name: /test-report\.xml · 212 KB/ })).toHaveAttribute('href', '/api/v1/repos/acme/api/actions/artifacts/a1/download');
    expect(screen.getByRole('button', { name: /Re-run/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Cancel run/ })).not.toBeInTheDocument();
  });

  it('in corso: il log si aggiorna da solo senza ricaricare', async () => {
    const running = run({ status: 'in_progress', conclusion: null, completedAt: null, jobs: [{ ...run({}).jobs[1]!, status: 'in_progress', conclusion: null, completedAt: null }] });
    setup(running);
    vi.mocked(fetchJobLog).mockResolvedValueOnce('=== RUN   TestWebhooks').mockResolvedValue('=== RUN   TestWebhooks\nPASS: TestWebhooks');
    expect(await screen.findByText(/=== RUN/, undefined, { timeout: 5000 })).toBeInTheDocument();
    expect(await screen.findByText(/PASS: TestWebhooks/, undefined, { timeout: 5000 })).toBeInTheDocument();
    expect(vi.mocked(fetchJobLog).mock.calls.length).toBeGreaterThan(1);
  });

  it('fetchRun che fallisce una volta: avviso, il polling riparte e il log continua ad aggiornarsi', async () => {
    const running = run({ status: 'in_progress', conclusion: null, completedAt: null, jobs: [{ ...run({}).jobs[1]!, status: 'in_progress', conclusion: null, completedAt: null }] });
    setup(running);
    vi.mocked(fetchRun).mockReset();
    // Il tentativo dopo l'errore resta in sospeso finché il test non lo rilascia: l'avviso è osservabile anche sotto carico.
    let release: () => void = () => undefined;
    const gate = new Promise<WorkflowRun>((resolve) => {
      release = () => resolve(running);
    });
    vi.mocked(fetchRun).mockResolvedValueOnce(running).mockRejectedValueOnce(new Error('boom')).mockReturnValueOnce(gate).mockResolvedValue(running);
    let n = 0;
    vi.mocked(fetchJobLog).mockImplementation(() => Promise.resolve(`riga ${++n}`));
    expect(await screen.findByText(/Aggiornamento interrotto, nuovo tentativo/, undefined, { timeout: 4000 })).toBeInTheDocument();
    release();
    await waitFor(() => expect(screen.queryByText(/Aggiornamento interrotto/)).not.toBeInTheDocument(), { timeout: 4000 });
    expect(vi.mocked(fetchRun).mock.calls.length).toBeGreaterThanOrEqual(3);
    const before = n;
    await waitFor(() => expect(n).toBeGreaterThan(before + 1), { timeout: 4000 });
  }, 15000);

  it('artefatti: una lettura all’apertura e una al passaggio a completed, non a ogni giro', async () => {
    const running = run({ status: 'in_progress', conclusion: null, completedAt: null });
    setup(running);
    vi.mocked(fetchRun).mockReset();
    vi.mocked(fetchRun).mockResolvedValue(running);
    await screen.findByRole('heading', { name: 'ci' }, { timeout: 5000 });
    await waitFor(() => expect(vi.mocked(fetchRun).mock.calls.length).toBeGreaterThanOrEqual(6), { timeout: 4000 });
    expect(fetchArtifacts).toHaveBeenCalledTimes(1);
    vi.mocked(fetchRun).mockResolvedValue(run({}));
    await waitFor(() => expect(fetchArtifacts).toHaveBeenCalledTimes(2), { timeout: 4000 });
    await new Promise((r) => setTimeout(r, 150));
    expect(fetchArtifacts).toHaveBeenCalledTimes(2);
  }, 15000);

  it('Annulla: senza conferma nessuna chiamata, con conferma la chiamata parte', async () => {
    const user = userEvent.setup();
    setup(run({ status: 'in_progress', conclusion: null, completedAt: null }));
    vi.mocked(cancelRun).mockResolvedValue(run({ status: 'cancelled', conclusion: 'cancelled' }));
    await user.click(await screen.findByRole('button', { name: 'Cancel run' }, { timeout: 5000 }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Go back' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(cancelRun).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: 'Cancel run' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel run' }));
    await waitFor(() => expect(cancelRun).toHaveBeenCalledWith('acme', 'api', 'run1'));
    expect(cancelRun).toHaveBeenCalledTimes(1);
  });

  it('Riesegui parte senza conferma e porta alla nuova esecuzione', async () => {
    const user = userEvent.setup();
    setup(run({}));
    vi.mocked(rerunRun).mockResolvedValue(run({ id: 'run2', status: 'queued', conclusion: null }));
    await user.click(await screen.findByRole('button', { name: /Re-run/ }, { timeout: 5000 }));
    await waitFor(() => expect(rerunRun).toHaveBeenCalledWith('acme', 'api', 'run1'));
    expect(await screen.findByText('new run')).toBeInTheDocument();
  });

  it('senza write non ci sono Riesegui né Annulla', async () => {
    setup(run({}), false);
    await screen.findByRole('heading', { name: 'ci' }, { timeout: 5000 });
    expect(screen.queryByRole('button', { name: /Re-run/ })).not.toBeInTheDocument();
  });
});
