import { ApiError } from '../../lib/http';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { AppsResourcesPage } from './AppsResourcesPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/adminAppsApi', () => ({
  fetchAdminAppsData: vi.fn(), updateAppsReserve: vi.fn(), saveOwnerQuota: vi.fn(), resetOwnerQuota: vi.fn(),
  fetchOwnerQuotas: vi.fn(), fetchPublicApps: vi.fn(), saveAppsSettings: vi.fn(), saveAppsEgress: vi.fn(), addTrustedKey: vi.fn(), removeTrustedKey: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/adminAppsApi';

const ownerQuota = { owner: 'acme', limits: { cpuMillis: 3000, memoryBytes: 6 * 1024 ** 3, diskBytes: 50 * 1024 ** 3 }, used: { cpuMillis: 2500, memoryBytes: 4 * 1024 ** 3, diskBytes: 18 * 1024 ** 3 }, warning: true };
const settings = { appsDomain: 'apps.acme.it', publicAppsEnabled: false, signedImagesOnly: false, restricted: false, maxPerApp: { cpuMillis: 4000, memoryBytes: 8 * 1024 ** 3 }, logRetention: { days: 7, maxBytes: 1024 ** 3 } };
const data = {
  capacity: { server: { cpuMillis: 8000, memoryBytes: 16 * 1024 ** 3, profile: 'recommended' }, services: { reserved: { cpuMillis: 2000, memoryBytes: 5 * 1024 ** 3 } }, apps: { share: { cpuMillis: 4000, memoryBytes: 8 * 1024 ** 3 }, guaranteed: { cpuMillis: 3300, memoryBytes: 6 * 1024 ** 3 } }, ci: { guaranteedCpuMillis: 2000, usedCpuMillis: 1000, slots: 3 }, appsShareWarning: false },
  egress: { privateDestinations: [{ host: 'smtp.acme.lan', port: 587, note: 'mail relay' }] },
  quotas: [ownerQuota], settings, keys: [{ id: 'k1', name: 'Instance CI key', fingerprint: 'SHA256:abc', instanceKey: true, createdAt: '2026-10-01T00:00:00Z' }],
  apps: [{ owner: 'acme', repo: 'handbook', name: 'web', status: 'running', copies: 2, guaranteed: { cpuMillis: 1000, memoryBytes: 1024 ** 3 }, limit: { cpuMillis: 2000, memoryBytes: 2 * 1024 ** 3 }, usage: { cpuMillis: 1240, memoryBytes: 820 * 1024 ** 2 }, verified: true, runsAsRoot: false, aboveGuaranteed: true }],
};

let publicApps: unknown[] = [];

function setup(isAdmin = true, resources = data) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(api.fetchAdminAppsData).mockResolvedValue(resources as never);
  vi.mocked(api.fetchPublicApps).mockResolvedValue(publicApps as never);
  return render(<ToastProvider><AppsResourcesPage /></ToastProvider>);
}

describe('AppsResourcesPage', { timeout: 30000 }, () => {
  beforeEach(() => { vi.clearAllMocks(); publicApps = []; });

  it('mostra capacità, quote, app e impostazioni H9', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Apps & resources' })).toBeInTheDocument();
    expect(screen.getByText('acme/handbook/web')).toBeInTheDocument();
    expect(screen.getByText('Quotas by owner')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Public apps' })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Only signed images' })).not.toBeChecked();
  });

  it('saves instance settings through the admin API', async () => {
    vi.mocked(api.saveAppsSettings).mockResolvedValue({ ...settings, signedImagesOnly: true });
    const user = userEvent.setup(); setup();
    const toggle = await screen.findByRole('switch', { name: 'Only signed images' });
    await user.click(toggle);
    await waitFor(() => expect(api.saveAppsSettings).toHaveBeenCalledWith({ publicAppsEnabled: false, signedImagesOnly: true, restricted: false, maxPerApp: settings.maxPerApp, logRetention: settings.logRetention }));
  });

  it('saves owner quota through the admin API', async () => {
    vi.mocked(api.saveOwnerQuota).mockResolvedValue(ownerQuota);
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: 'Edit' }));
    const dialog = screen.getByRole('form', { name: 'Edit quota for acme' });
    await user.clear(within(dialog).getByLabelText('Guaranteed CPU (vCPU)'));
    await user.type(within(dialog).getByLabelText('Guaranteed CPU (vCPU)'), '4');
    await user.click(within(dialog).getByRole('button', { name: 'Save quota' }));
    await waitFor(() => expect(api.saveOwnerQuota).toHaveBeenCalledWith('acme', { cpuMillis: 4000, memoryBytes: 6 * 1024 ** 3, diskBytes: 50 * 1024 ** 3 }));
  });

  it('adds a trusted key through the admin API', async () => {
    vi.mocked(api.addTrustedKey).mockResolvedValue({ id: 'k2', name: 'ci', fingerprint: 'SHA256:def', instanceKey: false, createdAt: '2026-10-26T00:00:00Z' });
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: 'Add key' }));
    const form = screen.getByRole('form', { name: 'Add trusted key' });
    await user.type(within(form).getByLabelText('Name'), 'ci');
    await user.type(within(form).getByLabelText('Public key (PEM)'), 'PEM...');
    await user.click(within(form).getByRole('button', { name: 'Add key' }));
    await waitFor(() => expect(api.addTrustedKey).toHaveBeenCalledWith('ci', 'PEM...'));
  });

  it('removes a non-instance trusted key and keeps the instance key non-removable', async () => {
    const user = userEvent.setup();
    const resources = { ...data, keys: [
      ...data.keys,
      { id: 'k2', name: 'repo CI', fingerprint: 'SHA256:def', instanceKey: false, createdAt: '2026-10-26T00:00:00Z' },
    ] };
    setup(true, resources);
    expect(await screen.findByText('Instance CI key')).toBeInTheDocument();
    const keys = screen.getByText('Trusted keys').parentElement!;
    expect(within(keys).getAllByRole('button', { name: 'Remove' })).toHaveLength(1);
    await user.click(within(keys).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(api.removeTrustedKey).toHaveBeenCalledWith('k2'));
    expect(within(keys).queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
    expect(within(keys).getByText('Instance CI key')).toBeInTheDocument();
  });

  it('saves the apps share with CPU and memory converted to API units', async () => {
    vi.mocked(api.updateAppsReserve).mockResolvedValue(data.capacity as never);
    const user = userEvent.setup(); setup();
    await screen.findByRole('heading', { name: 'Apps & resources' });
    await user.clear(screen.getByRole('spinbutton', { name: 'Apps share CPU' }));
    await user.type(screen.getByRole('spinbutton', { name: 'Apps share CPU' }), '3.5');
    await user.clear(screen.getByRole('spinbutton', { name: 'Apps share memory' }));
    await user.type(screen.getByRole('spinbutton', { name: 'Apps share memory' }), '6');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.updateAppsReserve).toHaveBeenCalledWith({ cpuMillis: 3500, memoryBytes: 6 * 1024 ** 3 }));
  });

  it('resets a custom owner quota and reloads the owner with instance defaults', async () => {
    const defaultQuota = { ...ownerQuota, isDefault: true, limits: { cpuMillis: 5000, memoryBytes: 10 * 1024 ** 3, diskBytes: 100 * 1024 ** 3 }, warning: false };
    vi.mocked(api.resetOwnerQuota).mockResolvedValue();
    vi.mocked(api.fetchOwnerQuotas).mockResolvedValue([defaultQuota] as never);
    const user = userEvent.setup(); setup();
    await screen.findByText('Quotas by owner');
    const quotaSection = screen.getByText('Quotas by owner').parentElement!;
    expect(within(quotaSection).getByRole('button', { name: 'Reset' })).toBeInTheDocument();
    await user.click(within(quotaSection).getByRole('button', { name: 'Reset' }));
    await waitFor(() => expect(api.resetOwnerQuota).toHaveBeenCalledWith('acme'));
    await waitFor(() => expect(api.fetchOwnerQuotas).toHaveBeenCalled());
    expect(within(quotaSection).getByText('acme')).toBeInTheDocument();
    expect(within(quotaSection).getByText(/5 vCPU/)).toBeInTheDocument();
    expect(within(quotaSection).queryByRole('button', { name: 'Reset' })).not.toBeInTheDocument();
  });

  it('does not load admin resources for non-admins', async () => {
    setup(false);
    expect(await screen.findByText('Only installation administrators can manage app resources.')).toBeInTheDocument();
    expect(api.fetchAdminAppsData).not.toHaveBeenCalled();
  });
  it('shows the private destinations opened for app outbound and no longer static text (H16)', async () => {
    setup();
    expect(await screen.findByText('smtp.acme.lan:587')).toBeInTheDocument();
    expect(screen.getByText(/mail relay/)).toBeInTheDocument();
    expect(screen.getByText(/private network closed/)).toBeInTheDocument();
    expect(screen.queryByText('Configured by instance egress policy')).not.toBeInTheDocument();
    expect(screen.queryByText('Managed in the shared egress allow-list')).not.toBeInTheDocument();
  });

  it('opens a private destination through the admin API, keeping the ones already open', async () => {
    const saved = { privateDestinations: [{ host: 'smtp.acme.lan', port: 587, note: 'mail relay' }, { host: 'db.azienda.lan', port: 5432, note: '' }] };
    vi.mocked(api.saveAppsEgress).mockResolvedValue(saved as never);
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: '+ Add destination' }));
    const dialog = screen.getByRole('form', { name: 'Add private destination' });
    await user.type(within(dialog).getByLabelText('Host'), 'db.azienda.lan');
    await user.type(within(dialog).getByLabelText('Port'), '5432');
    await user.click(within(dialog).getByRole('button', { name: 'Open destination' }));
    await waitFor(() => expect(api.saveAppsEgress).toHaveBeenCalledWith([{ host: 'smtp.acme.lan', port: 587, note: 'mail relay' }, { host: 'db.azienda.lan', port: 5432, note: '' }]));
    expect(await screen.findByText('db.azienda.lan:5432')).toBeInTheDocument();
  });

  it('shows the API refusal for a destination that cannot be opened', async () => {
    vi.mocked(api.saveAppsEgress).mockRejectedValue(new ApiError({ error: { code: 'destination_blocked', message: 'Destinazione non ammessa: 10.43.0.1:443 is inside the cluster network' } }, 422));
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: '+ Add destination' }));
    const dialog = screen.getByRole('form', { name: 'Add private destination' });
    await user.type(within(dialog).getByLabelText('Host'), '10.43.0.1');
    await user.type(within(dialog).getByLabelText('Port'), '443');
    await user.click(within(dialog).getByRole('button', { name: 'Open destination' }));
    expect(await screen.findByText(/inside the cluster network/)).toBeInTheDocument();
  });

  it('closes a private destination', async () => {
    vi.mocked(api.saveAppsEgress).mockResolvedValue({ privateDestinations: [] } as never);
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: 'Close smtp.acme.lan:587' }));
    await waitFor(() => expect(api.saveAppsEgress).toHaveBeenCalledWith([]));
    expect(await screen.findByText(/None opened/)).toBeInTheDocument();
  });

  it('asks confirmation, explains the double consent and turns public apps on', async () => {
    vi.mocked(api.saveAppsSettings).mockResolvedValue({ ...settings, publicAppsEnabled: true });
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('switch', { name: 'Public apps' }));
    const dialog = screen.getByRole('dialog', { name: 'Turn on public apps' });
    expect(within(dialog).getByText(/two consents/)).toBeInTheDocument();
    expect(api.saveAppsSettings).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Turn on' }));
    await waitFor(() => expect(api.saveAppsSettings).toHaveBeenCalledWith({ publicAppsEnabled: true, signedImagesOnly: false, restricted: false, maxPerApp: settings.maxPerApp, logRetention: settings.logRetention }));
    expect(await screen.findByRole('switch', { name: 'Public apps' })).toBeChecked();
  });

  it('cancelling the confirmation changes nothing', async () => {
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('switch', { name: 'Public apps' }));
    await user.click(within(screen.getByRole('dialog', { name: 'Turn on public apps' })).getByRole('button', { name: 'Cancel' }));
    expect(api.saveAppsSettings).not.toHaveBeenCalled();
    expect(screen.getByRole('switch', { name: 'Public apps' })).not.toBeChecked();
  });

  it('shows Let’s Encrypt with port 80 not reachable', async () => {
    const letsEncrypt = { enabled: true, acmeEmail: 'ops@acme.it', port80Reachable: false, port80Error: 'connection timed out', publicIps: ['203.0.113.5'] };
    setup(true, { ...data, settings: { ...settings, publicAppsEnabled: true, letsEncrypt } } as never);
    expect(await screen.findByText(/Account email: ops@acme.it/)).toBeInTheDocument();
    expect(screen.getByText('not reachable')).toBeInTheDocument();
    expect(screen.getByText(/connection timed out/)).toBeInTheDocument();
  });

  it('shows port 80 reachable', async () => {
    const letsEncrypt = { enabled: true, acmeEmail: 'ops@acme.it', port80Reachable: true };
    setup(true, { ...data, settings: { ...settings, publicAppsEnabled: true, letsEncrypt } } as never);
    expect(await screen.findByText('reachable')).toBeInTheDocument();
  });

  it('saves the ACME email', async () => {
    vi.mocked(api.saveAppsSettings).mockResolvedValue(settings as never);
    const user = userEvent.setup(); setup();
    await user.type(await screen.findByLabelText('ACME email'), 'ops@acme.it');
    await user.click(screen.getByRole('button', { name: 'Save email' }));
    await waitFor(() => expect(api.saveAppsSettings).toHaveBeenCalledWith(expect.objectContaining({ acmeEmail: 'ops@acme.it', publicAppsEnabled: false })));
  });

  it('shows an empty public apps list', async () => {
    setup();
    expect(await screen.findByText('No public apps on this instance.')).toBeInTheDocument();
  });

  it('lists public apps with domains and certificate state', async () => {
    publicApps = [{ owner: 'acme', repo: 'handbook', name: 'web', address: null, domains: [
      { id: 'd1', domain: 'www.acme.it', status: 'verified', certificate: { mode: 'letsencrypt', state: 'valid', validUntil: '2027-01-01T00:00:00Z' } },
      { id: 'd2', domain: 'shop.acme.it', status: 'verified', certificate: { mode: 'letsencrypt', state: 'error', error: 'challenge failed' } },
    ] }];
    setup();
    const list = (await screen.findByText('www.acme.it')).closest('section')!;
    expect(within(list).getByText('acme/handbook/web')).toBeInTheDocument();
    expect(within(list).getByText('valid')).toBeInTheDocument();
    expect(within(list).getByText('shop.acme.it')).toBeInTheDocument();
    expect(within(list).getByText(/challenge failed/)).toBeInTheDocument();
  });
});
