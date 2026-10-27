import { ApiError } from '../../../lib/http';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../../components';
import type { App, AppVolumeList } from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import { AppVolumesPage } from './AppVolumesPage';
import { makeApp, makeVersion, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchAppVolumes: vi.fn(),
  fetchAppVersions: vi.fn(),
  addAppVolume: vi.fn(),
  removeAppVolume: vi.fn(),
  setVolumeInBackup: vi.fn(),
  saveAppResources: vi.fn(),
  updateApp: vi.fn(),
  fetchAppEgress: vi.fn(),
  saveAppEgress: vi.fn(),
  clearAppEgress: vi.fn(),
}));

import * as api from '../../../lib/appsApi';

const T = 20000;
const GB = 1024 ** 3;
const quota = {
  owner: 'acme',
  limits: { cpuMillis: 4000, memoryBytes: 8 * GB, diskBytes: 50 * GB },
  used: { cpuMillis: 500, memoryBytes: GB, diskBytes: 15 * GB },
  warning: false,
};
const vol = (name: string, mountPath: string, size: number, includeInBackup = true) => ({
  name,
  mountPath,
  sizeBytes: size * GB,
  version: 3,
  createdAt: '2026-10-26T10:00:00Z',
  includeInBackup,
  looksLikeDatabase: /postgres|mysql|mongo/.test(mountPath),
});
const list = (...items: ReturnType<typeof vol>[]): AppVolumeList => ({ items, quota });
const withVersion = (restricted: boolean) => ({ items: [makeVersion({ number: 42, state: 'serving', config: { variables: {}, secretNames: [], resources: makeApp().resources, copies: 1, restricted } })], total: 1 });

function mount(app: App = makeApp({ copies: 1, resources: { guaranteed: { cpuMillis: 500, memoryBytes: 768 * 1024 ** 2 }, limit: { cpuMillis: 1000, memoryBytes: 1.5 * GB }, maxPerApp: { cpuMillis: 4000, memoryBytes: 8 * GB } } }), role: 'admin' | 'write' = 'admin') {
  const onChange = vi.fn();
  render(
    <MemoryRouter>
      <ToastProvider>
        <AppVolumesPage repo={repo} app={app} perms={appPermissions(role)} onChange={onChange} />
      </ToastProvider>
    </MemoryRouter>,
  );
  return onChange;
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.fetchAppVersions).mockResolvedValue(withVersion(false));
  vi.mocked(api.fetchAppEgress).mockResolvedValue({ restricted: false, destinations: [], allowedPrivate: [] });
});

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="volumes".
describe('AppVolumesPage (H3, H9, H11)', () => {
  it('elenca i volumi con mount, dimensione, quota e interruttore dei backup', async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list(vol('data', '/wiki/data', 10)));
    mount();
    const rows = within(await screen.findByRole('list', { name: 'Volumes' }));
    expect(rows.getByText('data')).toBeInTheDocument();
    expect(rows.getByText('/wiki/data')).toBeInTheDocument();
    expect(rows.getByText(/10 GB · counts toward the acme disk quota/)).toBeInTheDocument();
    expect(rows.getByRole('switch', { name: 'data in backups' })).toBeChecked();
    expect(screen.getByText(/Apps with volumes run as a single copy/)).toBeInTheDocument();
    expect(screen.getByText(/acme disk quota: 15 GB of 50 GB/)).toBeInTheDocument();
    expect(screen.queryByText(/Looks like a database/)).not.toBeInTheDocument();
  }, T);

  it('avvisa per un database e indica il volume dove fare il dump', async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list(vol('data', '/wiki/data', 10), vol('db', '/var/lib/postgresql/data', 5)));
    mount();
    expect(await screen.findByText(/Looks like a database/)).toBeInTheDocument();
    expect(screen.getByText(/Schedule a dump into the/)).toHaveTextContent('data');
    // «How to» porta alla guida al dump di docs/operations.md (D19).
    expect(screen.getByRole('link', { name: 'How to' })).toHaveAttribute('href', expect.stringContaining('docs/operations.md#volumes-and-databases'));
  }, T);

  it("l'avviso database viene dall'API, non dal solo percorso", async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list({ ...vol('data', '/wiki/data', 10), looksLikeDatabase: true }));
    mount();
    expect(await screen.findByText(/Looks like a database/)).toBeInTheDocument();
  }, T);

  it("l'interruttore dei backup riflette includeInBackup e lo cambia (D19)", async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValueOnce(list(vol('data', '/wiki/data', 10), vol('cache', '/cache', 1, false))).mockResolvedValue(list(vol('data', '/wiki/data', 10, false), vol('cache', '/cache', 1, false)));
    vi.mocked(api.setVolumeInBackup).mockResolvedValue({} as never);
    mount();
    const rows = within(await screen.findByRole('list', { name: 'Volumes' }));
    expect(rows.getByRole('switch', { name: 'data in backups' })).toBeChecked();
    expect(rows.getByRole('switch', { name: 'cache in backups' })).not.toBeChecked();
    await user.click(rows.getByRole('switch', { name: 'data in backups' }));
    await waitFor(() => expect(api.setVolumeInBackup).toHaveBeenCalledWith('acme', 'handbook', 'web', 'data', false));
    await waitFor(() => expect(within(screen.getByRole('list', { name: 'Volumes' })).getByRole('switch', { name: 'data in backups' })).not.toBeChecked());
  }, T);

  it("senza admin l'interruttore dei backup è bloccato", async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list(vol('data', '/wiki/data', 10)));
    mount(undefined, 'write');
    const rows = within(await screen.findByRole('list', { name: 'Volumes' }));
    expect(rows.getByRole('switch', { name: 'data in backups' })).toBeDisabled();
  }, T);

  it('copia unica: le copie sono fisse a 1 quando ci sono volumi', async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list(vol('data', '/wiki/data', 10)));
    mount();
    expect(await screen.findByText('Fixed at 1 because the app has volumes')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More copies' })).toBeDisabled();
  }, T);

  it('aggiunge un volume con dimensione in GB e ricarica la lista', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValueOnce(list()).mockResolvedValue(list(vol('data', '/wiki/data', 10)));
    vi.mocked(api.addAppVolume).mockResolvedValue({} as never);
    mount();
    await user.click(await screen.findByRole('button', { name: 'Add volume' }));
    const form = within(screen.getByRole('form', { name: 'Add volume' }));
    await user.type(form.getByLabelText('Volume name'), 'data');
    await user.type(form.getByLabelText('Mount path'), '/wiki/data');
    await user.type(form.getByLabelText('Size'), '10');
    await user.click(form.getByRole('button', { name: 'Add volume' }));
    await waitFor(() => expect(api.addAppVolume).toHaveBeenCalledWith('acme', 'handbook', 'web', { name: 'data', mountPath: '/wiki/data', sizeBytes: 10 * GB }));
    expect(await screen.findByText('/wiki/data')).toBeInTheDocument();
  }, T);

  it('mostra in linea il rifiuto per quota piena (409)', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    const { ApiError } = await vi.importActual<typeof import('../../../lib/http')>('../../../lib/http');
    vi.mocked(api.addAppVolume).mockRejectedValue(new ApiError({ error: { code: 'insufficient_capacity', message: 'Not enough disk in the acme quota.' } }, 409));
    mount();
    await user.click(await screen.findByRole('button', { name: 'Add volume' }));
    const form = within(screen.getByRole('form', { name: 'Add volume' }));
    await user.type(form.getByLabelText('Volume name'), 'big');
    await user.type(form.getByLabelText('Mount path'), '/big');
    await user.type(form.getByLabelText('Size'), '100 GB');
    await user.click(form.getByRole('button', { name: 'Add volume' }));
    expect(await screen.findByText(/Not enough disk/)).toBeInTheDocument();
  }, T);

  it('toglie un volume solo riscrivendone il nome (G8)', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValueOnce(list(vol('data', '/wiki/data', 10))).mockResolvedValue(list());
    vi.mocked(api.removeAppVolume).mockResolvedValue({} as never);
    mount();
    await user.click(await screen.findByRole('button', { name: 'Remove volume data' }));
    const dialog = within(await screen.findByRole('dialog'));
    const confirm = dialog.getByRole('button', { name: 'Remove volume' });
    expect(confirm).toBeDisabled();
    await user.type(dialog.getByRole('textbox'), 'data');
    await user.click(confirm);
    await waitFor(() => expect(api.removeAppVolume).toHaveBeenCalledWith('acme', 'handbook', 'web', 'data'));
    expect(await screen.findByText('No volumes')).toBeInTheDocument();
  }, T);

  it('salva risorse garantite e tetto, rifiutando oltre il massimo dell’istanza', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.saveAppResources).mockImplementation(async (_o, _r, _a, input) => ({ ...input, maxPerApp: null }));
    mount();
    const cpu = await screen.findByLabelText('Limit CPU');
    expect(screen.getByLabelText('Guaranteed CPU')).toHaveValue('0.5 vCPU');
    expect(screen.getByLabelText('Guaranteed memory')).toHaveValue('768 MB');
    expect(screen.getByText(/Max per app on this instance: 4 vCPU · 8 GB/)).toBeInTheDocument();
    await user.clear(cpu);
    await user.type(cpu, '9');
    await user.click(screen.getByRole('button', { name: 'Save resources' }));
    expect(await screen.findByText(/above the maximum per app/)).toBeInTheDocument();
    expect(api.saveAppResources).not.toHaveBeenCalled();
    await user.clear(cpu);
    await user.type(cpu, '2');
    await user.click(screen.getByRole('button', { name: 'Save resources' }));
    await waitFor(() =>
      expect(api.saveAppResources).toHaveBeenCalledWith('acme', 'handbook', 'web', {
        guaranteed: { cpuMillis: 500, memoryBytes: 768 * 1024 ** 2 },
        limit: { cpuMillis: 2000, memoryBytes: 1.5 * GB },
      }),
    );
  }, T);

  it('restricted: legge lo stato dalla versione attiva e lo cambia; avviso se gira come root', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.updateApp).mockResolvedValue(makeApp());
    const root = makeApp({ copies: 1, image: { reference: 'docker.io/requarks/wiki:2.5', source: 'external', verified: false, runsAsRoot: true } });
    mount(root);
    const sw = await screen.findByRole('switch', { name: 'Restricted mode' });
    expect(sw).not.toBeChecked();
    expect(screen.getByText(/runs as/)).toHaveTextContent('root');
    await user.click(sw);
    await waitFor(() => expect(api.updateApp).toHaveBeenCalledWith('acme', 'handbook', 'web', { restricted: true }));
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Restricted mode' })).toBeChecked());
  }, T);

  it('con solo write tutto è in sola lettura', async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list(vol('data', '/wiki/data', 10)));
    mount(makeApp({ copies: 1 }), 'write');
    await screen.findByText('/wiki/data');
    expect(screen.queryByRole('button', { name: 'Add volume' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Remove volume data' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save resources' })).not.toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Restricted mode' })).toBeDisabled();
  }, T);
});

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="volumes", «Outbound network» (H16).
describe('AppVolumesPage · Outbound network (H16)', () => {
  const opened = [{ host: 'smtp.acme.lan', port: 587, note: '' }];

  it("mostra internet aperto, la rete privata chiusa con le destinazioni dell'amministratore e le sempre bloccate", async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.fetchAppEgress).mockResolvedValue({ restricted: false, destinations: [], allowedPrivate: opened });
    mount();
    const section = within(await screen.findByRole('region', { name: 'Outbound network' }));
    expect(section.getByText(/open · through the company proxy/)).toBeInTheDocument();
    expect(section.getByText('smtp.acme.lan:587')).toBeInTheDocument();
    expect(section.getByText(/loopback, cluster network, link-local, Kubernetes ports/)).toBeInTheDocument();
    expect(section.getByRole('switch', { name: 'Restrict this app' })).not.toBeChecked();
    expect(section.queryByRole('list', { name: 'Allowed destinations' })).not.toBeInTheDocument();
  }, T);

  it("restringe l'app, aggiunge e toglie destinazioni", async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.fetchAppEgress).mockResolvedValue({ restricted: false, destinations: [], allowedPrivate: opened });
    vi.mocked(api.saveAppEgress)
      .mockResolvedValueOnce({ restricted: true, destinations: [], allowedPrivate: opened })
      .mockResolvedValueOnce({ restricted: true, destinations: [{ host: 'smtp.acme.lan', port: 587 }], allowedPrivate: opened })
      .mockResolvedValueOnce({ restricted: true, destinations: [], allowedPrivate: opened });
    mount();
    const section = within(await screen.findByRole('region', { name: 'Outbound network' }));
    await user.click(section.getByRole('switch', { name: 'Restrict this app' }));
    await waitFor(() => expect(api.saveAppEgress).toHaveBeenCalledWith('acme', 'handbook', 'web', []));
    expect(await section.findByText(/No destination listed/)).toBeInTheDocument();
    await user.type(section.getByLabelText('Host'), 'smtp.acme.lan');
    await user.type(section.getByLabelText('Port'), '587');
    await user.click(section.getByRole('button', { name: 'Add destination' }));
    await waitFor(() => expect(api.saveAppEgress).toHaveBeenLastCalledWith('acme', 'handbook', 'web', [{ host: 'smtp.acme.lan', port: 587 }]));
    const row = within(await section.findByRole('list', { name: 'Allowed destinations' }));
    expect(row.getByText('smtp.acme.lan:587')).toBeInTheDocument();
    await user.click(row.getByRole('button', { name: 'Remove smtp.acme.lan:587' }));
    await waitFor(() => expect(api.saveAppEgress).toHaveBeenLastCalledWith('acme', 'handbook', 'web', []));
  }, T);

  it("mostra il rifiuto dell'API per una destinazione fuori dalla lista dell'amministratore", async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.fetchAppEgress).mockResolvedValue({ restricted: true, destinations: [], allowedPrivate: [] });
    vi.mocked(api.saveAppEgress).mockRejectedValue(new ApiError({ error: { code: 'destination_blocked', message: 'Destinazione non ammessa: 10.20.0.99:5432 is in the private network' } }, 422));
    mount();
    const section = within(await screen.findByRole('region', { name: 'Outbound network' }));
    await user.type(section.getByLabelText('Host'), '10.20.0.99');
    await user.type(section.getByLabelText('Port'), '5432');
    await user.click(section.getByRole('button', { name: 'Add destination' }));
    expect(await section.findByText(/Destinazione non ammessa/)).toBeInTheDocument();
  }, T);

  it('toglie la restrizione', async () => {
    const user = userEvent.setup();
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    vi.mocked(api.fetchAppEgress)
      .mockResolvedValueOnce({ restricted: true, destinations: [{ host: 'api.example.com', port: 443 }], allowedPrivate: [] })
      .mockResolvedValue({ restricted: false, destinations: [], allowedPrivate: [] });
    vi.mocked(api.clearAppEgress).mockResolvedValue(undefined);
    mount();
    const section = within(await screen.findByRole('region', { name: 'Outbound network' }));
    expect(section.getByText('api.example.com:443')).toBeInTheDocument();
    await user.click(section.getByRole('switch', { name: 'Restrict this app' }));
    await waitFor(() => expect(api.clearAppEgress).toHaveBeenCalledWith('acme', 'handbook', 'web'));
    await waitFor(() => expect(section.getByRole('switch', { name: 'Restrict this app' })).not.toBeChecked());
  }, T);

  it('senza admin la restrizione è in sola lettura', async () => {
    vi.mocked(api.fetchAppVolumes).mockResolvedValue(list());
    mount(undefined, 'write');
    const section = within(await screen.findByRole('region', { name: 'Outbound network' }));
    expect(section.getByRole('switch', { name: 'Restrict this app' })).toBeDisabled();
  }, T);
});
