import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { appPermissions } from '../../../lib/appsPermissions';
import type { AppRole } from '../../../lib/appsPermissions';
import * as api from '../../../lib/appsApi';
import { AppConfigPage } from './AppConfigPage';
import { AppLogsPage } from './AppLogsPage';
import { makeApp, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchAppLogs: vi.fn(), fetchAppVariables: vi.fn(), fetchAppSecrets: vi.fn(),
  saveAppVariable: vi.fn(), saveAppSecret: vi.fn(), removeAppVariable: vi.fn(), removeAppSecret: vi.fn(),
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

// Mockup: .prisma/design/mockups-v3-hosting/index.html, data-screen="logs".
describe('AppLogsPage (H4)', () => {
  it('mostra i log del visore condiviso e filtra per copia/intervallo', async () => {
    mount(<AppLogsPage repo={repo} app={makeApp()} perms={perms('write')} />);
    expect(await screen.findByText('deploy v43 started')).toBeInTheDocument();
    expect(screen.getByRole('log', { name: 'App logs' })).toBeInTheDocument();
    expect(api.fetchAppLogs).toHaveBeenCalledWith('acme', 'handbook', 'web', expect.objectContaining({ source: 'all', since: '1h' }));
  });

  it('read non vede i log e non invia richieste', () => {
    mount(<AppLogsPage repo={repo} app={makeApp()} perms={perms('read')} />);
    expect(screen.getByText('Logs require write access to this repository.')).toBeInTheDocument();
    expect(api.fetchAppLogs).not.toHaveBeenCalled();
  });
});

// Mockup: .prisma/design/mockups-v3-hosting/index.html, data-screen="config".
describe('AppConfigPage (H6)', () => {
  it('read e write non possono gestire variabili o segreti', () => {
    for (const role of ['read', 'write'] as const) {
      const { unmount } = mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms(role)} />);
      expect(screen.queryByRole('button', { name: 'New variable' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'New secret' })).not.toBeInTheDocument();
      expect(api.fetchAppSecrets).not.toHaveBeenCalled();
      unmount();
    }
  });

  it('admin salva e sostituisce un segreto senza mostrare il valore salvato', async () => {
    vi.mocked(api.saveAppSecret).mockResolvedValue();
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('admin')} />);
    expect(await screen.findByText('SEARCH_URL')).toBeInTheDocument();
    expect(await screen.findByText('DATABASE_URL')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Update' }));
    const form = screen.getByLabelText('Value');
    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), '');
    await userEvent.type(form, 'super-secret-value');
    await userEvent.click(screen.getByRole('button', { name: 'Save secret' }));
    await waitFor(() => expect(api.saveAppSecret).toHaveBeenCalledWith('acme', 'handbook', 'web', 'DATABASE_URL', 'super-secret-value'));
    expect(screen.queryByText('super-secret-value')).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue('super-secret-value')).not.toBeInTheDocument();
  });

  it('segreti mostrano solo nome e metadati, mai il valore', async () => {
    mount(<AppConfigPage repo={repo} app={makeApp()} perms={perms('admin')} />);
    const row = await screen.findByText('DATABASE_URL');
    expect(within(row.closest('.list-row') as HTMLElement).queryByDisplayValue('sensitive-secret')).not.toBeInTheDocument();
  });
});
