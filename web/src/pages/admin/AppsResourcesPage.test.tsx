import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { AppsResourcesPage } from './AppsResourcesPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/adminAppsApi', () => ({
  fetchAdminAppsData: vi.fn(), updateAppsReserve: vi.fn(), saveOwnerQuota: vi.fn(), resetOwnerQuota: vi.fn(),
  saveAppsSettings: vi.fn(), addTrustedKey: vi.fn(), removeTrustedKey: vi.fn(),
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

function setup(isAdmin = true) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(api.fetchAdminAppsData).mockResolvedValue(data as never);
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

  it('does not load admin resources for non-admins', async () => {
    setup(false);
    expect(await screen.findByText('Only installation administrators can manage app resources.')).toBeInTheDocument();
    expect(api.fetchAdminAppsData).not.toHaveBeenCalled();
  });
});
