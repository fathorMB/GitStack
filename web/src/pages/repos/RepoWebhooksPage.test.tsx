import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { RepoWebhooksPage } from './RepoWebhooksPage';

/* ========== Mocks ========== */

vi.mock('../../lib/reposApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/reposApi')>()),
  fetchRepo: vi.fn(),
}));

vi.mock('../../lib/authApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/authApi')>()),
  fetchSession: vi.fn().mockResolvedValue({ user: { username: 'mrossi', isAdmin: false, kind: 'human' as const } }),
}));

vi.mock('../../lib/orgsApi', () => ({
  fetchOrgMembers: vi.fn().mockResolvedValue([{ user: { username: 'mrossi' }, role: 'owner' }]),
}));

vi.mock('./repoAdmin', async (orig) => ({
  ...(await orig<typeof import('./repoAdmin')>()),
  isRepoAdmin: vi.fn().mockResolvedValue(true),
  loadMe: vi.fn().mockResolvedValue({ username: 'mrossi', isAdmin: false, kind: 'human' as const }),
}));

vi.mock('../../lib/webhooksApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/webhooksApi')>()),
  fetchRepoWebhooks: vi.fn(),
  createRepoWebhook: vi.fn(),
  updateRepoWebhook: vi.fn(),
  deleteRepoWebhook: vi.fn(),
  reactivateRepoWebhook: vi.fn(),
  fetchRepoWebhookDeliveries: vi.fn(),
  redeliverRepoWebhookDelivery: vi.fn(),
}));

import { fetchRepo } from '../../lib/reposApi';
import { fetchRepoWebhooks, createRepoWebhook, reactivateRepoWebhook, fetchRepoWebhookDeliveries, redeliverRepoWebhookDelivery } from '../../lib/webhooksApi';
import { isRepoAdmin, loadMe } from './repoAdmin';
import { fetchOrgMembers } from '../../lib/orgsApi';
import type { Webhook, WebhookDeliverySummary } from '../../lib/webhooksApi';

const mockedFetchRepo = vi.mocked(fetchRepo);
const mockedFetch = vi.mocked(fetchRepoWebhooks);
const mockedCreate = vi.mocked(createRepoWebhook);
const mockedReactivate = vi.mocked(reactivateRepoWebhook);
const mockedDeliveries = vi.mocked(fetchRepoWebhookDeliveries);
const mockedRedeliver = vi.mocked(redeliverRepoWebhookDelivery);
const mockedLoadMe = vi.mocked(loadMe);
const mockedOrgMembers = vi.mocked(fetchOrgMembers);

const NOW = new Date().toISOString();

const FAKE_REPO = {
  id: 'r-1',
  owner: { type: 'organization' as const, name: 'acme' },
  name: 'api-gateway',
  fullName: 'acme/api-gateway',
  description: 'Test repo',
  visibility: 'private' as const,
  defaultBranch: 'main',
  protectDefaultBranch: true,
  archived: false,
  archivedAt: null,
  empty: false,
  cloneUrls: { https: 'https://git.example.com/acme/api-gateway.git', ssh: 'ssh://git@git.example.com:2222/acme/api-gateway.git' },
  createdAt: '2025-01-01T00:00:00Z',
  updatedAt: '2025-10-05T00:00:00Z',
};

/* ========== Tests ========== */

describe('RepoWebhooksPage', () => {
  beforeEach(() => {
    mockedFetchRepo.mockResolvedValue(FAKE_REPO);
    mockedFetch.mockResolvedValue({ items: [], page: 1, perPage: 50, total: 0 });
    mockedReactivate.mockResolvedValue({
      id: 'wh-2', scope: 'repo', repository: { id: 'r-2', fullName: 'acme/api-gateway' }, organization: null,
      url: 'https://old-ci.acme.local/hooks/gitstack', events: ['push'], active: true, hasSecret: false,
      disabledAt: null, disabledReason: undefined, failingSince: null, lastDelivery: null,
      createdBy: null, createdAt: '2025-09-01T00:00:00Z', updatedAt: '2025-10-01T00:00:00Z',
    });
    mockedDeliveries.mockResolvedValue({ items: [], page: 1, perPage: 20, total: 0 });
    mockedRedeliver.mockResolvedValue({} as never);
    mockedLoadMe.mockResolvedValue({ username: 'mrossi', isAdmin: false, kind: 'human' as const });
    mockedOrgMembers.mockResolvedValue([{ user: { username: 'mrossi' }, role: 'owner' }]);
    vi.mocked(isRepoAdmin).mockResolvedValue(true);
    vi.mocked(mockedCreate).mockReset();
  });

  function renderPage(webhookList?: { items: Webhook[]; page: number; perPage: number; total: number }, isAdmin?: boolean) {
    if (webhookList !== undefined) {
      mockedFetch.mockResolvedValue(webhookList);
    }
    if (isAdmin !== undefined) {
      vi.mocked(isRepoAdmin).mockResolvedValue(isAdmin);
    }
    return render(
      <ToastProvider>
        <MemoryRouter initialEntries={['/acme/api-gateway/webhooks']}>
          <Routes>
            <Route path="/:owner/:repo/webhooks" element={<RepoWebhooksPage />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>,
    );
  }

  it('mostra la lista webhook attivi', async () => {
    mockedFetch.mockResolvedValue({ items: [
      {
        id: 'wh-1', scope: 'repo', repository: { id: 'r-1', fullName: 'acme/api-gateway' }, organization: null,
        url: 'https://ci.acme.local/hooks/gitstack', events: ['push', 'issues'], active: true, hasSecret: true,
        disabledAt: null, disabledReason: undefined, failingSince: null,
        lastDelivery: { id: 'd-1', event: 'push', status: 'success', attempt: 1, statusCode: 200, durationMs: 82, createdAt: NOW, deliveredAt: NOW },
        createdBy: null, createdAt: NOW, updatedAt: NOW,
      } as Webhook,
    ], page: 1, perPage: 50, total: 1 });
    renderPage();

    expect(await screen.findByText('Webhooks')).toBeInTheDocument();
    expect(screen.getByText('ci.acme.local/hooks/gitstack')).toBeInTheDocument();
  });

  it('mostra EmptyState quando non ci sono webhook', async () => {
    renderPage({ items: [], page: 1, perPage: 50, total: 0 });

    await waitFor(() => {
      expect(mockedFetchRepo).toHaveBeenCalled();
    }, { timeout: 3000 });
    expect(await screen.findByText('No webhooks')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add webhook' })).toBeInTheDocument();
  });

  it('mostra webhook disattivato con pulsante Re-enable', async () => {
    mockedFetch.mockResolvedValue({ items: [
      {
        id: 'wh-2', scope: 'repo', repository: { id: 'r-2', fullName: 'acme/api-gateway' }, organization: null,
        url: 'https://old-ci.acme.local/hooks/gitstack', events: ['push'], active: false, hasSecret: false,
        disabledAt: '2025-10-01T00:00:00Z', disabledReason: 'too_many_failures', failingSince: '2025-09-28T00:00:00Z',
        lastDelivery: null, createdBy: null, createdAt: '2025-09-01T00:00:00Z', updatedAt: '2025-10-01T00:00:00Z',
      } as Webhook,
    ], page: 1, perPage: 50, total: 1 });
    renderPage();

    expect(await screen.findByText('Webhooks')).toBeInTheDocument();
    expect(screen.getByText('old-ci.acme.local/hooks/gitstack')).toBeInTheDocument();
    const buttons = screen.getAllByRole('button');
    expect(buttons.some((b) => b.textContent === 'Re-enable')).toBe(true);
  });

  it('conferma e attiva un webhook disattivato', async () => {
    mockedFetch.mockResolvedValue({ items: [
      {
        id: 'wh-2', scope: 'repo', repository: { id: 'r-2', fullName: 'acme/api-gateway' }, organization: null,
        url: 'https://old-ci.acme.local/hooks/gitstack', events: ['push'], active: false, hasSecret: false,
        disabledAt: '2025-10-01T00:00:00Z', disabledReason: 'too_many_failures', failingSince: '2025-09-28T00:00:00Z',
        lastDelivery: null, createdBy: null, createdAt: '2025-09-01T00:00:00Z', updatedAt: '2025-10-01T00:00:00Z',
      } as Webhook,
    ], page: 1, perPage: 50, total: 1 });
    const user = userEvent.setup();
    renderPage();

    await screen.findByText('Webhooks');
    const reenableBtn = screen.getByRole('button', { name: 'Re-enable' });
    await user.click(reenableBtn);
    expect(mockedReactivate).toHaveBeenCalledWith('acme', 'api-gateway', 'wh-2');
  });

  it('mostra il testo informativo su GitStack v1', async () => {
    renderPage();
    expect(await screen.findByText('GitStack sends a signed POST')).toBeInTheDocument();
    expect(screen.getByText(/X-GitStack-Event/)).toBeInTheDocument();
    expect(screen.getByText(/X-GitStack-Signature/)).toBeInTheDocument();
    expect(screen.getByText(/8 times over 24 hours/)).toBeInTheDocument();
    expect(screen.getByText(/disabled automatically/)).toBeInTheDocument();
  });

  it('mostra il form di creazione', async () => {
    mockedCreate.mockResolvedValue({
      id: 'wh-new', scope: 'repo', repository: { id: 'r-1', fullName: 'acme/api-gateway' }, organization: null,
      url: 'https://new.example.com/hooks', events: ['push'], active: true, hasSecret: true,
      disabledAt: null, disabledReason: undefined, failingSince: null,
      lastDelivery: null, createdBy: null, createdAt: NOW, updatedAt: NOW,
    } as Webhook);
    const user = userEvent.setup();
    renderPage();

    await screen.findByText('No webhooks');
    await user.click(screen.getByRole('button', { name: 'Add webhook' }));
    await waitFor(() => {
      expect(screen.getByLabelText('Payload URL')).toBeInTheDocument();
    }, { timeout: 5000 });
    const urlInput = screen.getByLabelText('Payload URL');
    await user.clear(urlInput);
    await user.type(urlInput, 'https://new.example.com/hooks');
    await user.click(screen.getByRole('button', { name: 'Create webhook' }));
    await waitFor(() => {
      expect(mockedCreate).toHaveBeenCalledWith('acme', 'api-gateway', {
        url: 'https://new.example.com/hooks',
        events: [],
      });
    }, { timeout: 5000 });
    expect(await screen.findByText('Webhook created')).toBeInTheDocument();
  }, { timeout: 15000 });

  it('mostra consegne recenti con Redeliver', async () => {
    mockedFetch.mockResolvedValue({ items: [
      {
        id: 'wh-1', scope: 'repo', repository: { id: 'r-1', fullName: 'acme/api-gateway' }, organization: null,
        url: 'https://ci.acme.local/hooks/gitstack', events: ['push', 'issues'], active: true, hasSecret: true,
        disabledAt: null, disabledReason: undefined, failingSince: null,
        lastDelivery: { id: 'd-1', event: 'push', status: 'success', attempt: 1, statusCode: 200, durationMs: 82, createdAt: NOW, deliveredAt: NOW },
        createdBy: null, createdAt: NOW, updatedAt: NOW,
      } as Webhook,
    ], page: 1, perPage: 50, total: 1 });
    mockedDeliveries.mockResolvedValue({
      items: [
        { id: 'd123456789abcdef01', event: 'push', status: 'success', attempt: 1, statusCode: 200, durationMs: 45, createdAt: NOW, deliveredAt: NOW } as WebhookDeliverySummary,
        { id: 'd223456789abcdef02', event: 'issues', status: 'failed', attempt: 2, statusCode: 500, durationMs: 5000, createdAt: NOW, deliveredAt: null } as WebhookDeliverySummary,
        { id: 'd323456789abcdef03', event: 'push', status: 'failed', attempt: 3, statusCode: 504, durationMs: 10000, createdAt: NOW, deliveredAt: null } as WebhookDeliverySummary,
      ],
      page: 1,
      perPage: 20,
      total: 3,
    });
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await waitFor(() => {
      expect(screen.getByText('Recent deliveries')).toBeInTheDocument();
    }, { timeout: 5000 });
    expect(screen.getByText('d123456789abcdef01')).toBeInTheDocument();
    expect(screen.getByText('d223456789abcdef02')).toBeInTheDocument();
    expect(screen.getByText('d323456789abcdef03')).toBeInTheDocument();
    const redeliverButtons = screen.getAllByRole('button', { name: 'Redeliver' });
    expect(redeliverButtons.length).toBeGreaterThanOrEqual(3);
  }, { timeout: 15000 });

  it('non mostra la sezione Webhooks senza permessi admin', async () => {
    renderPage(undefined, false);
    const message = await screen.findByText('Webhooks are available only to repository admins.');
    expect(message).toBeInTheDocument();
    expect(screen.queryByText('Webhooks')).not.toBeInTheDocument();
  });

  it('mostra errore quando il server risponde con 501', async () => {
    mockedFetch.mockRejectedValue(
      new ApiError({ error: { code: 'not_implemented', message: 'webhook API not implemented' } }, 501),
    );
    renderPage();
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.getByText(/Could not reach the server/)).toBeInTheDocument();
  });

  it('mostra il messaggio di successo per Redeliver', async () => {
    mockedFetch.mockResolvedValue({ items: [
      {
        id: 'wh-1', scope: 'repo', repository: { id: 'r-1', fullName: 'acme/api-gateway' }, organization: null,
        url: 'https://ci.acme.local/hooks/gitstack', events: ['push', 'issues'], active: true, hasSecret: true,
        disabledAt: null, disabledReason: undefined, failingSince: null,
        lastDelivery: { id: 'd-1', event: 'push', status: 'success', attempt: 1, statusCode: 200, durationMs: 82, createdAt: NOW, deliveredAt: NOW },
        createdBy: null, createdAt: NOW, updatedAt: NOW,
      } as Webhook,
    ], page: 1, perPage: 50, total: 1 });
    mockedDeliveries.mockResolvedValue({
      items: [{ id: 'd-1', event: 'push', status: 'success', attempt: 1, statusCode: 200, durationMs: 82, createdAt: NOW, deliveredAt: NOW } as WebhookDeliverySummary],
      page: 1,
      perPage: 20,
      total: 1,
    });
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await waitFor(() => {
      expect(screen.getByText('Recent deliveries')).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Redeliver' }));
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Queued' })).toBeInTheDocument();
    });
  });

  it('mostra il dialog del segreto dopo la creazione', async () => {
    mockedCreate.mockResolvedValue({
      id: 'wh-new', scope: 'repo', repository: { id: 'r-1', fullName: 'acme/api-gateway' }, organization: null,
      url: 'https://new.example.com/hooks', events: ['push'], active: true, hasSecret: true,
      disabledAt: null, disabledReason: undefined, failingSince: null,
      lastDelivery: null, createdBy: null, createdAt: NOW, updatedAt: NOW,
    } as Webhook);
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => {
      expect(mockedFetchRepo).toHaveBeenCalled();
    }, { timeout: 3000 });
    await screen.findByText('No webhooks');
    await user.click(screen.getByRole('button', { name: 'Add webhook' }));
    await waitFor(() => {
      expect(screen.getByLabelText('Payload URL')).toBeInTheDocument();
    }, { timeout: 5000 });
    const urlInput = screen.getByLabelText('Payload URL');
    await user.clear(urlInput);
    await user.type(urlInput, 'https://new.example.com/hooks');
    await user.click(screen.getByRole('button', { name: 'Create webhook' }));
    await waitFor(() => {
      expect(screen.getByText('Webhook created')).toBeInTheDocument();
    }, { timeout: 5000 });
  }, { timeout: 15000 });
});
