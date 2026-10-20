import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRun } from '../../lib/actionsApi';
import type { Repository } from '../../lib/reposApi';
import { ActionsPage } from './ActionsPage';

vi.mock('../../lib/actionsApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/actionsApi')>()),
  fetchWorkflows: vi.fn(),
  fetchRuns: vi.fn(),
  dispatchWorkflow: vi.fn(),
}));
vi.mock('../../lib/useCanWrite', () => ({ useCanWrite: vi.fn() }));

import { dispatchWorkflow, fetchRuns, fetchWorkflows } from '../../lib/actionsApi';
import { useCanWrite } from '../../lib/useCanWrite';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', archived: false, defaultBranch: 'main' } as Repository;

const mk = (over: Partial<WorkflowRun>): WorkflowRun => ({
  id: 'r',
  workflowId: 'ci',
  status: 'completed',
  conclusion: 'success',
  event: 'push',
  ref: 'refs/heads/main',
  sha: 'abcdef1234567',
  createdAt: '2026-10-19T10:00:00Z',
  startedAt: '2026-10-19T10:00:00Z',
  completedAt: '2026-10-19T10:08:40Z',
  jobs: [],
  ...over,
});

function setup(write = true) {
  vi.mocked(useCanWrite).mockReturnValue(write);
  vi.mocked(fetchWorkflows).mockResolvedValue([
    { id: 'ci', path: '.github/workflows/ci.yml', name: 'ci', state: 'active' },
    { id: 'nightly', path: '.gitstack/workflows/nightly.yml', name: 'nightly-e2e', state: 'active' },
  ]);
  vi.mocked(fetchRuns).mockResolvedValue([
    mk({ id: '4', status: 'in_progress', conclusion: null, completedAt: null, event: 'pull_request', ref: 'refs/heads/feat/x' }),
    mk({ id: '3', conclusion: 'failure', ref: 'refs/heads/fix/y' }),
    mk({ id: '2', status: 'queued', conclusion: null, startedAt: null, completedAt: null }),
    mk({ id: '1', workflowId: 'nightly', event: 'schedule' }),
  ]);
  return render(
    <MemoryRouter>
      <ActionsPage repo={repo} />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.mocked(dispatchWorkflow).mockReset();
});

describe('ActionsPage', () => {
  it('elenca esecuzioni con stato, coda e link al dettaglio', async () => {
    setup();
    const list = await screen.findByRole('list', { name: 'Workflow runs' });
    expect(within(list).getAllByRole('listitem')).toHaveLength(4);
    expect(within(list).getByText('Failed · 8m 40s')).toBeInTheDocument();
    expect(within(list).getByText('Queue: 1 running · 1 waiting')).toBeInTheDocument();
    expect(within(list).getAllByRole('link', { name: 'ci' })[0]).toHaveAttribute('href', '/acme/api/actions/runs/4');
  });

  it('filtra per Failed, per testo e per workflow', async () => {
    const user = userEvent.setup();
    setup();
    const list = await screen.findByRole('list', { name: 'Workflow runs' });
    await user.click(screen.getByRole('button', { name: 'Failed' }));
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'All' }));
    await user.type(screen.getByLabelText('Filter runs'), 'event:schedule');
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
    await user.clear(screen.getByLabelText('Filter runs'));
    await user.click(within(screen.getByRole('navigation', { name: 'Workflows' })).getByRole('link', { name: /nightly-e2e/ }));
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
  });

  it('avvio a mano: ramo e input, solo con write', async () => {
    const user = userEvent.setup();
    vi.mocked(dispatchWorkflow).mockResolvedValue(undefined);
    setup();
    await user.click(await screen.findByRole('button', { name: /Run workflow/ }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Use workflow from')).toHaveValue('main');
    await user.click(within(dialog).getByRole('button', { name: /Add input/ }));
    await user.type(within(dialog).getByLabelText('Input name'), 'target_os');
    await user.type(within(dialog).getByLabelText('Value of target_os'), 'ubuntu-24.04');
    await user.click(within(dialog).getByRole('button', { name: /Run workflow/ }));
    await waitFor(() => expect(dispatchWorkflow).toHaveBeenCalledWith('acme', 'api', 'ci', 'main', { target_os: 'ubuntu-24.04' }));
  });

  it('senza write non c’è il pulsante di avvio', async () => {
    setup(false);
    await screen.findByRole('list', { name: 'Workflow runs' });
    expect(screen.queryByRole('button', { name: /Run workflow/ })).not.toBeInTheDocument();
  });
});
