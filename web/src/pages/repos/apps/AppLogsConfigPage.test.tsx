import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { appPermissions } from '../../../lib/appsPermissions';
import type { AppRole } from '../../../lib/appsPermissions';
import * as api from '../../../lib/appsApi';
import { AppConfigPage } from './AppConfigPage';
import { AppLogsPage } from './AppLogsPage';
import { makeApp, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchAppLogs: vi.fn(), fetchAppVariables: vi.fn(), fetchAppSecrets: vi.fn(),
  saveAppVariable: vi.fn(), saveAppSecret: vi.fn(), removeAppVariable: vi.fn(), removeAppSecret: vi.fn(), removeApp: vi.fn(),
}));
vi.mock('../../../components', async (original) => ({
  ...await original<typeof import('../../../components')>(),
  useToast: () => ({ toast: vi.fn() }),
}));

const mount = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>);
const perms = (role: AppRole) => appPermissions(role);

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchAppLogs).mockResolvedValue([{ time: '2026-10-25T10:00:00Z', source: 'platform', message: 'deploy v43 started' }]);
  vi.mocked(api.fetchAppVariables).mockResolvedValue({ items: [{ name: 'SEARCH_URL', value: 'http://search:9200', managed: false, updatedAt: '2026-10-24T10:00:00Z' }] });
  vi.mocked(api.fetchAppSecrets).mockResolvedValue({ items: [{ name: 'DATABASE_URL', updatedAt: '2026-10-24T10:00:00Z', updatedBy: { id: 'u1', username: 'mrossi', type: 'human' } }] });
});
afterEach(() => vi.useRealTimers());

// Mockup: .prisma/design/mockups-v3-hosting/index.html, data-screen="logs".
describe('AppLogsPage (H4)', () => {
  it('in live interroga subito e ripete dopo tre secondi senza aprire uno stream', async () => {
    vi.useFakeTimers();
    mount(<AppLogsPage repo={repo} app={makeApp()} perms={perms('write')} />);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(api.fetchAppLogs).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.fetchAppLogs).mock.calls[0][3]).toMatchObject({ since: '1h' });
    expect(vi.mocked(api.fetchAppLogs).mock.calls[0][3]).not.toHaveProperty('follow');
    await act(async () => { vi.advanceTimersByTime(3000); await Promise.resolve(); await Promise.resolve(); });
    expect(api.fetchAppLogs).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.fetchAppLogs).mock.calls[1][3]).not.toHaveProperty('follow');
  });

  it('read non vede i log e non invia richieste', () => {
    mount(<AppLogsPage repo={repo} app={makeApp()} perms={perms('read')} />);
    expect(screen.getByText('Logs require write access to this repository.')).toBeInTheDocument();
    expect(api.fetchAppLogs).not.toHaveBeenCalled();
  });
});

// Mockup: .prisma/design/mockups-v3-hosting/index.html, data-screen="config".
describe('AppConfigPage (H6)', () => {
  it('read non legge variabili e vede il gate write', () => {
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('read')} />);
    expect(screen.getByText('Variables and secrets require write access to this repository.')).toBeInTheDocument();
    expect(api.fetchAppVariables).not.toHaveBeenCalled();
    expect(api.fetchAppSecrets).not.toHaveBeenCalled();
  });

  it('write legge variabili ma non gestisce variabili o segreti', async () => {
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('write')} />);
    expect(await screen.findByText('SEARCH_URL')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'New variable' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit variable SEARCH_URL' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete variable SEARCH_URL' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'New secret' })).not.toBeInTheDocument();
    expect(api.fetchAppSecrets).not.toHaveBeenCalled();
  });

  it('admin vede i controlli di gestione', async () => {
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('admin')} />);
    expect(await screen.findByText('SEARCH_URL')).toBeInTheDocument();
    expect(await screen.findByText('DATABASE_URL')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New variable' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Edit variable SEARCH_URL' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete variable SEARCH_URL' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New secret' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete secret DATABASE_URL' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete app' })).toBeInTheDocument();
  });

  it('dopo aver sostituito un segreto il valore non resta nella pagina', async () => {
    vi.mocked(api.saveAppSecret).mockResolvedValue();
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('admin')} />);
    expect(await screen.findByText('DATABASE_URL')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Update' }));
    await userEvent.type(screen.getByLabelText('Value'), 'super-secret-value');
    await userEvent.click(screen.getByRole('button', { name: 'Save secret' }));
    await waitFor(() => expect(api.saveAppSecret).toHaveBeenCalledWith('acme', 'handbook', 'web', 'DATABASE_URL', 'super-secret-value'));
    await waitFor(() => expect(api.fetchAppSecrets).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('super-secret-value')).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue('super-secret-value')).not.toBeInTheDocument();
  });

  it('non mostra un eventuale campo value ricevuto per errore nei metadati', async () => {
    vi.mocked(api.fetchAppSecrets).mockResolvedValueOnce({
      items: [{ name: 'DATABASE_URL', updatedAt: '2026-10-24T10:00:00Z', value: 'leak' } as unknown as Awaited<ReturnType<typeof api.fetchAppSecrets>>['items'][number]],
    });
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('admin')} />);
    expect(await screen.findByText('DATABASE_URL')).toBeInTheDocument();
    expect(screen.queryByText('leak')).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue('leak')).not.toBeInTheDocument();
  });
});
