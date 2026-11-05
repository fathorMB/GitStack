import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { DatabasesSettingsPage } from './DatabasesSettingsPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/adminDatabasesApi', () => ({
  fetchAdminDatabasesData: vi.fn(), saveDatabasesSettings: vi.fn(), saveDatabaseOwnerQuota: vi.fn(), resetDatabaseOwnerQuota: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import * as api from '../../lib/adminDatabasesApi';

const GiB = 1024 ** 3;
const settings = { enabled: true, allowedMajors: [16, 17], maxPerDatabase: { sizeBytes: 100 * GiB, cpuMillis: 2000, memoryBytes: 4 * GiB }, lanPortRange: { from: 15432, to: 15531 }, backupRetention: { scheduled: 7, onDemand: 5 } };
const quota = { owner: 'acme', limits: { cpuMillis: 3000, memoryBytes: 6 * GiB, diskBytes: 50 * GiB }, used: { cpuMillis: 100, memoryBytes: GiB, diskBytes: 2 * GiB }, isDefault: false };
const databases = [
  { id: '1', owner: 'acme', name: 'erp', pgMajor: 16, status: 'running', sizeBytes: 5 * GiB, cpuMillis: 500, memoryBytes: GiB, lanEnabled: true, lanPort: 15440, linkCount: 1, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', operations: { minorUpdateAvailable: false, lastBackupAt: '2026-11-02T03:00:00Z' } },
  { id: '2', owner: 'alice', name: 'shop', pgMajor: 17, status: 'running', sizeBytes: GiB, cpuMillis: 100, memoryBytes: 256 * 1024 ** 2, lanEnabled: false, linkCount: 0, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', operations: { minorUpdateAvailable: true, backupError: 'pg_dump: connection refused' } },
];
const data = { disabled: false, settings, databases, quotas: [quota], errors: {} };

function setup(isAdmin = true, loaded: unknown = data) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(api.fetchAdminDatabasesData).mockResolvedValue(loaded as never);
  return render(<ToastProvider><DatabasesSettingsPage /></ToastProvider>);
}

describe('DatabasesSettingsPage', { timeout: 30000 }, () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('mostra stato, versioni, limiti, quote e l\'elenco dei database dell\'istanza', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Database settings' })).toBeInTheDocument();
    expect(screen.getByText('acme/erp')).toBeInTheDocument();
    expect(screen.getByText('alice/shop')).toBeInTheDocument();
    expect(screen.getByText('2 databases · 1 failed backup')).toBeInTheDocument();
    expect(screen.getByText('backup failed')).toBeInTheDocument();
    expect(screen.getByText('update available')).toBeInTheDocument();
    expect(screen.getByText('port 15440')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'PostgreSQL 16' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'PostgreSQL 17' })).toBeChecked();
    expect(screen.getByLabelText('Max CPU')).toHaveValue(2);
    expect(screen.getByLabelText('LAN port from')).toHaveValue(15432);
    expect(screen.getByText('Quotas by owner')).toBeInTheDocument();
    expect(screen.getByText(/Shared with apps/)).toBeInTheDocument();
  });

  it('con la funzione spenta dice come accenderla, senza errori', async () => {
    setup(true, { disabled: true, settings: null, databases: null, quotas: null, errors: {} });
    expect(await screen.findByText('Managed databases are off')).toBeInTheDocument();
    expect(screen.getByText(/gitstack config set databases on/)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('una sezione che non si carica mostra il suo errore e lascia le altre', async () => {
    setup(true, { ...data, databases: null, errors: { databases: 'Something went wrong' } });
    expect(await screen.findByText('Databases: Something went wrong')).toBeInTheDocument();
    expect(screen.getByLabelText('Max CPU')).toBeInTheDocument();
  });

  it('salva le impostazioni d\'istanza con l\'API admin', async () => {
    vi.mocked(api.saveDatabasesSettings).mockResolvedValue({ ...settings, allowedMajors: [17], lanPortRange: { from: 20000, to: 20010 } } as never);
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('checkbox', { name: 'PostgreSQL 16' }));
    const from = screen.getByLabelText('LAN port from'); await user.clear(from); await user.type(from, '20000');
    const to = screen.getByLabelText('LAN port to'); await user.clear(to); await user.type(to, '20010');
    await user.click(screen.getByRole('button', { name: 'Save settings' }));
    await waitFor(() => expect(api.saveDatabasesSettings).toHaveBeenCalledWith({
      enabled: true, allowedMajors: [17],
      maxPerDatabase: { sizeBytes: 100 * GiB, cpuMillis: 2000, memoryBytes: 4 * GiB },
      lanPortRange: { from: 20000, to: 20010 }, backupRetention: { scheduled: 7, onDemand: 5 },
    }));
  });

  it('mostra il rifiuto del server (major in uso)', async () => {
    vi.mocked(api.saveDatabasesSettings).mockRejectedValue(new Error('La major 16 e\' usata dal database acme/erp'));
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: 'Save settings' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
  });

  it('salva la quota per owner', async () => {
    vi.mocked(api.saveDatabaseOwnerQuota).mockResolvedValue(quota as never);
    const user = userEvent.setup(); setup();
    await user.click(await screen.findByRole('button', { name: 'Edit' }));
    const dialog = screen.getByRole('form', { name: 'Edit quota for acme' });
    await user.click(within(dialog).getByRole('button', { name: 'Save quota' }));
    await waitFor(() => expect(api.saveDatabaseOwnerQuota).toHaveBeenCalledWith('acme', { cpuMillis: 3000, memoryBytes: 6 * GiB, diskBytes: 50 * GiB }));
  });

  it('è riservata agli amministratori', async () => {
    setup(false);
    expect(await screen.findByText('Only installation administrators can manage database settings.')).toBeInTheDocument();
    expect(api.fetchAdminDatabasesData).not.toHaveBeenCalled();
  });
});
