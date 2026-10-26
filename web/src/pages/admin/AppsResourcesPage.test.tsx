import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { AppsResourcesPage } from './AppsResourcesPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/adminAppsApi', () => ({
  fetchAdminAppsData: vi.fn(), updateAppsReserve: vi.fn(), saveOwnerQuota: vi.fn(), resetOwnerQuota: vi.fn(),
  fetchOwnerQuotas: vi.fn(), saveAppsSettings: vi.fn(), addTrustedKey: vi.fn(), removeTrustedKey: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/adminAppsApi';

const ownerQuota = { owner: 'acme', limits: { cpuMillis: 3000, memoryBytes: 6 * 1024 ** 3, diskBytes: 50 * 1024 ** 3 }, used: { cpuMillis: 2500, memoryBytes: 4 * 1024 ** 3, diskBytes: 18 * 1024 ** 3 }, warning: true };
const settings = { appsDomain: 'apps.acme.it', publicAppsEnabled: false, signedImagesOnly: false, restricted: false, maxPerApp: { cpuMillis: 4000, memoryBytes: 8 * 1024 ** 3 }, logRetention: { days: 7, maxBytes: 1024 ** 3 } };
const data = {
  capacity: { server: { cpuMillis: 8000, memoryBytes: 16 * 1024 ** 3, profile: 'recommended' }, services: { reserved: { cpuMillis: 2000, memoryBytes: 5 * 1024 ** 3 } }, apps: { share: { cpuMillis: 4000, memoryBytes: 8 * 1024 ** 3 }, guaranteed: { cpuMillis: 3300, memoryBytes: 6 * 1024 ** 3 } }, ci: { guaranteedCpuMillis: 2000, usedCpuMillis: 1000, slots: 3 }, appsShareWarning: false },
  quotas: [ownerQuota], settings, keys: [{ id: 'k1', name: 'Instance CI key', fingerprint: 'SHA256:abc', instanceKey: true, createdAt: '2026-10-01T00:00:00Z' }],
  apps: [{ owner: 'acme', repo: 'handbook', name: 'web', status: 'running', copies: 2, guaranteed: { cpuMillis: 1000, memoryBytes: 1024 ** 3 }, limit: { cpuMillis: 2000, memoryBytes: 2 * 1024 ** 3 }, usage: { cpuMillis: 1240, memoryBytes: 820 * 1024 ** 2 }, verified: true, runsAsRoot: false, aboveGuaranteed: true }],
};

function setup(isAdmin = true, resources = data) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(api.fetchAdminAppsData).mockResolvedValue(resources as never);
  return render(<ToastProvider><AppsResourcesPage /></ToastProvider>);
}

describe('AppsResourcesPage', { timeout: 30000 }, () => {
  beforeEach(() => vi.clearAllMocks());

  it('mostra capacità, quote, app e impostazioni H9', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Apps & resources' })).toBeInTheDocument();
    expect(screen.getByText('acme/handbook/web')).toBeInTheDocument();
    expect(screen.getByText('Quotas by owner')).toBeInTheDocument();
    expect(screen.getByText(/v3-c/)).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Public apps' })).toBeDisabled();
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
});
