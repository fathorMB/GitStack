import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { AgentsPage } from './AgentsPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/agentsApi', () => ({
  fetchAgents: vi.fn(),
  createAgent: vi.fn(),
  deleteAgent: vi.fn(),
  setAgentActive: vi.fn(),
  fetchAgentTokens: vi.fn(),
  createAgentToken: vi.fn(),
  revokeAgentToken: vi.fn(),
  fetchAgentAccess: vi.fn(),
}));

import * as api from '../../lib/agentsApi';
import { fetchSession } from '../../lib/authApi';

const agent = (username: string) => ({
  id: `u-${username}`,
  username,
  kind: 'agent' as const,
  displayName: `${username} display`,
  isActive: true,
});
const token = {
  id: 't-1',
  name: 'ci-runner',
  scopes: ['write:resource' as const],
  hint: 'ab12',
  createdAt: '2026-09-01T00:00:00Z',
  expiresAt: '2026-12-01T00:00:00Z',
  lastUsedAt: null,
};

function setup(isAdmin = true) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(api.fetchAgents).mockResolvedValue([agent('build-agent'), agent('docs-agent')]);
  vi.mocked(api.fetchAgentTokens).mockResolvedValue([token]);
  vi.mocked(api.fetchAgentAccess).mockResolvedValue([
    { fullName: 'acme/api-gateway', role: 'write', from: 'via team acme/agents' },
    { fullName: 'acme/infra-charts', role: 'read', from: 'direct grant' },
    { fullName: 'acme/docs', role: 'read', from: 'internal repository' },
  ]);
  return render(
    <ToastProvider>
      <AgentsPage />
    </ToastProvider>,
  );
}

describe('AgentsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('non e accessibile a chi non e admin: nessuna chiamata agli agent', async () => {
    setup(false);
    expect(await screen.findByText(/Only installation administrators/)).toBeInTheDocument();
    expect(api.fetchAgents).not.toHaveBeenCalled();
    expect(screen.queryByText('New agent')).not.toBeInTheDocument();
  });

  it('elenca gli agent, i token dell agente scelto e l accesso via team', async () => {
    setup();
    expect(await screen.findByText('2 agents')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open docs-agent' })).toBeInTheDocument();
    expect(await screen.findByText('ci-runner')).toBeInTheDocument();
    expect(screen.getByText('write:resource')).toBeInTheDocument();
    expect(api.fetchAgentTokens).toHaveBeenCalledWith('build-agent');
    expect(api.fetchAgentAccess).toHaveBeenCalledWith('build-agent');
    expect(await screen.findByText('acme/api-gateway')).toBeInTheDocument();
    expect(screen.getByText('via team acme/agents')).toBeInTheDocument();
    expect(screen.getByText('direct grant')).toBeInTheDocument();
    expect(screen.getByText('internal repository')).toBeInTheDocument();
    expect(screen.getByText('Write')).toBeInTheDocument();
    expect(screen.getAllByText('Read')).toHaveLength(2);
  });

  it('crea un agent senza password', async () => {
    vi.mocked(api.createAgent).mockResolvedValue(agent('release-agent'));
    const user = userEvent.setup();
    setup();
    await screen.findByText('2 agents');
    await user.click(screen.getByRole('button', { name: /New agent/ }));
    const form = screen.getByRole('form', { name: 'New agent' });
    expect(within(form).queryByLabelText(/password/i)).not.toBeInTheDocument();
    await user.type(within(form).getByLabelText('Username'), 'release-agent');
    await user.click(within(form).getByRole('button', { name: 'Create agent' }));
    await waitFor(() => expect(api.createAgent).toHaveBeenCalledWith('release-agent', ''));
    expect(await screen.findByText('3 agents')).toBeInTheDocument();
  });

  it('elimina un agent solo dopo la conferma', async () => {
    vi.mocked(api.deleteAgent).mockResolvedValue();
    const user = userEvent.setup();
    setup();
    await screen.findByText('ci-runner');
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(api.deleteAgent).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('alertdialog').catch(() => screen.getByRole('dialog'));
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.deleteAgent).toHaveBeenCalledWith('build-agent'));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Open build-agent' })).not.toBeInTheDocument());
  });

  it('crea un token con scope e scadenza, il valore si vede una sola volta', async () => {
    vi.mocked(api.createAgentToken).mockResolvedValue({
      ...token,
      id: 't-2',
      name: 'dev-laptop',
      scopes: ['read:resource'],
      token: 'gst_SECRET123',
    });
    const user = userEvent.setup();
    setup();
    await screen.findByText('ci-runner');
    await user.click(screen.getByRole('button', { name: /Generate token/ }));
    const form = screen.getByRole('form', { name: 'New agent token' });
    await user.type(within(form).getByLabelText('Token name'), 'dev-laptop');
    await user.click(within(form).getByRole('button', { name: 'Create token' }));
    expect(await within(form).findByText('Select at least one scope.')).toBeInTheDocument();
    await user.click(within(form).getByRole('checkbox', { name: /read:resource/ }));
    await user.click(within(form).getByRole('button', { name: 'Create token' }));
    await waitFor(() => expect(api.createAgentToken).toHaveBeenCalledTimes(1));
    const [who, input] = vi.mocked(api.createAgentToken).mock.calls[0];
    expect(who).toBe('build-agent');
    expect(input).toMatchObject({ name: 'dev-laptop', scopes: ['read:resource'] });
    expect(input.expiresAt).toBeTruthy();
    expect(await screen.findByLabelText('New token value')).toHaveValue('gst_SECRET123');
    // nell'elenco il token compare senza valore
    expect(screen.getAllByText(/…ab12/)).toHaveLength(2);
    await user.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByDisplayValue('gst_SECRET123')).not.toBeInTheDocument();
    expect(screen.queryByText('gst_SECRET123')).not.toBeInTheDocument();
  });

  it('revoca un token dopo la conferma', async () => {
    vi.mocked(api.revokeAgentToken).mockResolvedValue();
    const user = userEvent.setup();
    setup();
    await screen.findByText('ci-runner');
    await user.click(screen.getByRole('button', { name: 'Revoke ci-runner' }));
    expect(api.revokeAgentToken).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('alertdialog').catch(() => screen.getByRole('dialog'));
    await user.click(within(dialog).getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(api.revokeAgentToken).toHaveBeenCalledWith('build-agent', 't-1'));
    await waitFor(() => expect(screen.queryByText('ci-runner')).not.toBeInTheDocument());
  });
});
