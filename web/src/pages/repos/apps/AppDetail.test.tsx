import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { App } from '../../../lib/appsApi';
import { AppAccessPage } from './AppAccessPage';
import { AppOverviewPage } from './AppOverviewPage';
import { AppVersionsPage } from './AppVersionsPage';
import { makeApp, makeVersion, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchApp: vi.fn(),
  fetchAppMetrics: vi.fn(),
  fetchAppVersions: vi.fn(),
  fetchAppAccess: vi.fn(),
  saveAppAccess: vi.fn(),
  fetchAppAccessMembers: vi.fn(),
  addAppAccessMember: vi.fn(),
  removeAppAccessMember: vi.fn(),
  fetchAppAccessInvite: vi.fn(),
  restartApp: vi.fn(),
  deployApp: vi.fn(),
  scaleApp: vi.fn(),
  rollbackApp: vi.fn(),
}));

import * as api from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import type { AppRole } from '../../../lib/appsPermissions';

const T = 20000;
const perms = (role: AppRole) => appPermissions(role);

function renderPage(ui: React.ReactElement) {
  render(<MemoryRouter>{ui}</MemoryRouter>);
}

const metrics = {
  available: true,
  range: '6h' as const,
  stepSeconds: 60,
  cpu: { unit: 'millicores' as const, points: [] },
  memory: { unit: 'bytes' as const, points: [] },
  requests: { unit: 'per_second' as const, points: [{ time: 't1', value: 10 }, { time: 't2', value: 40 }] },
  restarts24h: 0,
  copies: [],
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchAppMetrics).mockResolvedValue(metrics);
});

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="overview".
describe('AppOverviewPage (H3)', () => {
  const mount = (app: App, role: AppRole) => renderPage(<AppOverviewPage repo={repo} app={app} perms={perms(role)} onChange={vi.fn()} />);

  it('in esecuzione: versione, copie sane, richieste, errori, risorse e health check', async () => {
    mount(makeApp(), 'admin');
    expect(screen.getByText('v42')).toBeInTheDocument();
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    expect(screen.getByText('38 req/s')).toBeInTheDocument();
    expect(screen.getByText('0.1%')).toBeInTheDocument();
    expect(screen.getByText('0.62 vCPU')).toBeInTheDocument();
    expect(screen.getByText('410 MB')).toBeInTheDocument();
    expect(screen.getByText('GET /healthz')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('img', { name: 'Requests over the last 6 hours' })).toBeInTheDocument(), { timeout: T });
    expect(screen.queryByText(/failed its health check/)).not.toBeInTheDocument();
  }, T);

  it('deploy fallito con ritorno automatico: avviso con la versione che serve ancora', async () => {
    const app = makeApp({
      runningPreviousVersion: true,
      lastDeploy: { version: 43, state: 'rolled_back', startedAt: '2026-10-25T10:00:00Z', reason: 'GET /healthz returned 500 for 2 min' },
      usage: { cpuMillis: 100, memoryBytes: 1, requestsPerSecond: 35, errorRatio: 0.024 },
    });
    mount(app, 'write');
    expect(screen.getByText('Running previous version')).toBeInTheDocument();
    expect(screen.getByText(/Deploy of v43 failed its health check/)).toBeInTheDocument();
    expect(screen.getByText(/v42 is still serving traffic/)).toBeInTheDocument();
    expect(screen.getByText('2.4%')).toBeInTheDocument();
    const alert = screen.getByText(/Deploy of v43 failed/).closest('.alert') as HTMLElement;
    expect(within(alert).getByRole('link', { name: 'Versions' })).toHaveAttribute('href', '/acme/handbook/apps/web/versions');
    await waitFor(() => expect(api.fetchAppMetrics).toHaveBeenCalled(), { timeout: T });
  }, T);

  it('write: Restart, Deploy e copie; read: nessuno di questi', async () => {
    mount(makeApp(), 'write');
    expect(screen.getByRole('button', { name: /Restart/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Deploy/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More copies' })).toBeInTheDocument();
    await waitFor(() => expect(api.fetchAppMetrics).toHaveBeenCalled(), { timeout: T });
  }, T);

  it('read: nessun pulsante che cambia lo stato', async () => {
    mount(makeApp(), 'read');
    expect(screen.queryByRole('button', { name: /Restart/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Deploy/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More copies' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open/ })).toHaveAttribute('href', 'https://web-handbook-acme.apps.acme.it');
    await waitFor(() => expect(api.fetchAppMetrics).toHaveBeenCalled(), { timeout: T });
  }, T);

  it('Restart chiama l API e ricarica l app; una copia in piu usa scale', async () => {
    vi.mocked(api.restartApp).mockResolvedValue();
    vi.mocked(api.scaleApp).mockResolvedValue(makeApp({ copies: 3 }));
    vi.mocked(api.fetchApp).mockResolvedValue(makeApp());
    mount(makeApp(), 'write');
    await userEvent.click(screen.getByRole('button', { name: /Restart/ }));
    await waitFor(() => expect(api.restartApp).toHaveBeenCalledWith('acme', 'handbook', 'web'), { timeout: T });
    await userEvent.click(screen.getByRole('button', { name: 'More copies' }));
    await waitFor(() => expect(api.scaleApp).toHaveBeenCalledWith('acme', 'handbook', 'web', 3), { timeout: T });
  }, T);
});

// Mockup data-screen="versions".
describe('AppVersionsPage (H5)', () => {
  const versions = [
    makeVersion({ number: 43, state: 'rolled_back' }),
    makeVersion({ number: 42, state: 'serving' }),
    makeVersion({ number: 41, trigger: 'config', summary: 'variable SEARCH_TIMEOUT 2s → 5s', inBackup: true, createdBy: { id: 'u2', username: 'mrossi', type: 'human' } }),
    makeVersion({ number: 40, trigger: 'manual' }),
    makeVersion({ number: 39, imageAvailable: false }),
  ];
  const mount = (role: AppRole) => {
    vi.mocked(api.fetchAppVersions).mockResolvedValue({ items: versions, total: 5 });
    renderPage(<AppVersionsPage repo={repo} app={makeApp()} perms={perms(role)} onChange={vi.fn()} />);
  };

  it('storico: attiva, fallita con ritorno, in backup, immagine rimossa', async () => {
    mount('write');
    const list = await screen.findByRole('list', { name: 'Versions' }, { timeout: T });
    const rows = list.querySelectorAll('li');
    expect(rows).toHaveLength(5);
    expect(rows[0]).toHaveTextContent('failed health check · rolled back');
    expect(rows[1]).toHaveTextContent('active');
    expect(rows[1]).toHaveTextContent('verified');
    expect(rows[2]).toHaveTextContent('variable SEARCH_TIMEOUT');
    expect(rows[2]).toHaveTextContent('by mrossi');
    expect(rows[2]).toHaveTextContent('in backup');
    expect(rows[4]).toHaveTextContent('image removed by registry cleanup');
    expect(screen.getByRole('button', { name: 'Roll back to v41' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Roll back to v39' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Roll back to v42' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Roll back to v43' })).not.toBeInTheDocument();
  }, T);

  it('conferma del rollback: parte solo dopo la conferma, con la pausa dei deploy automatici', async () => {
    mount('write');
    vi.mocked(api.rollbackApp).mockResolvedValue(makeVersion({ number: 44 }));
    vi.mocked(api.fetchApp).mockResolvedValue(makeApp({ activeVersion: 44 }));
    await userEvent.click(await screen.findByRole('button', { name: 'Roll back to v41' }, { timeout: T }));
    expect(await screen.findByText('Roll back web to v41?', {}, { timeout: T })).toBeInTheDocument();
    expect(api.rollbackApp).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Roll back' }));
    await waitFor(() => expect(api.rollbackApp).toHaveBeenCalledWith('acme', 'handbook', 'web', 41, true), { timeout: T });
  }, T);

  it('annullare la conferma non fa il rollback', async () => {
    mount('write');
    await userEvent.click(await screen.findByRole('button', { name: 'Roll back to v40' }, { timeout: T }));
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }, { timeout: T }));
    expect(api.rollbackApp).not.toHaveBeenCalled();
  }, T);

  it('read: lo storico c e, i pulsanti Roll back no', async () => {
    mount('read');
    await screen.findByRole('list', { name: 'Versions' }, { timeout: T });
    expect(screen.queryByRole('button', { name: /Roll back/ })).not.toBeInTheDocument();
  }, T);
});

// Mockup data-screen="access", variante interna.
describe('AppAccessPage (H7, interna)', () => {
  const access = { mode: 'login' as const, publicAppsEnabled: false, address: 'web-handbook-acme.apps.acme.it', certificate: 'instance_ca' as const };
  const mount = (role: AppRole) => {
    vi.mocked(api.fetchAppAccess).mockResolvedValue(access);
    vi.mocked(api.fetchAppAccessMembers).mockResolvedValue([]);
    renderPage(<AppAccessPage repo={repo} app={makeApp()} perms={perms(role)} onChange={vi.fn()} />);
  };

  it('interna con pubbliche non abilitate: Public spento e dominio automatico con la CA', async () => {
    mount('admin');
    expect(await screen.findByRole('radio', { name: 'Internal · GitStack login' }, { timeout: T })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Public on the internet' })).toBeDisabled();
    expect(screen.getByText('disabled by the administrator')).toBeInTheDocument();
    expect(screen.getByText('GitStack CA')).toBeInTheDocument();
    expect(screen.getByText('automatic')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add domain/ })).not.toBeInTheDocument();
  }, T);

  it('admin cambia in Internal · no login', async () => {
    mount('admin');
    vi.mocked(api.saveAppAccess).mockResolvedValue({ ...access, mode: 'none' });
    await userEvent.click(await screen.findByRole('radio', { name: 'Internal · no login' }, { timeout: T }));
    await waitFor(() => expect(api.saveAppAccess).toHaveBeenCalledWith('acme', 'handbook', 'web', 'none'), { timeout: T });
  }, T);

  it.each(['read', 'write'] as const)('con %s le opzioni sono spente', async (role) => {
    mount(role);
    expect(await screen.findByRole('radio', { name: 'Internal · no login' }, { timeout: T })).toBeDisabled();
    expect(screen.getByText(/Only admins of the repository/)).toBeInTheDocument();
  }, T);
});
