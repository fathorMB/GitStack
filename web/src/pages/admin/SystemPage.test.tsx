import { render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AdminSystem } from '../../lib/systemApi';
import { SystemPage } from './SystemPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/systemApi', () => ({ fetchSystem: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import { fetchSystem } from '../../lib/systemApi';

const base: AdminSystem = {
  published: true,
  updatedAt: '2026-10-12T10:00:00Z',
  database: { mode: 'internal', serverVersion: '16.4', versionLive: true },
  backup: {
    destination: '/var/backups/gitstack',
    retention: 7,
    last: { state: 'success', at: '2026-10-12T02:00:00Z', name: '/var/backups/gitstack/gitstack-backup.tar.gz' },
    s3: { configured: false, upload: { state: 'none' } },
  },
  versions: { gitstack: 'v1.0.0', k3s: 'v1.36.4+k3s1', k3sDeclared: 'v1.36.4+k3s1', k3sUpdate: 'none' },
  network: { configured: false, corporateCA: false, mirrorCA: false, proxyHasCredentials: false, registryHasCredentials: false },
};

function setup(sys: AdminSystem, isAdmin = true) {
  vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'root', isAdmin } } as never);
  vi.mocked(fetchSystem).mockResolvedValue(sys);
  return render(<SystemPage />);
}

describe('SystemPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('non e accessibile a chi non e admin: nessuna chiamata a /admin/system', async () => {
    setup(base, false);
    expect(await screen.findByText(/only installation administrators/i)).toBeInTheDocument();
    expect(fetchSystem).not.toHaveBeenCalled();
  });

  it('database interno: versione letta ora, nessun host, rete non configurata', async () => {
    setup(base);
    expect(await screen.findByText('Internal (bundled)')).toBeInTheDocument();
    expect(screen.getByText('16.4')).toBeInTheDocument();
    expect(screen.getByText(/read now from the core connection/i)).toBeInTheDocument();
    expect(screen.queryByText('Host')).not.toBeInTheDocument();
    expect(screen.getByText(/not configured: no proxy, no mirror and no company ca/i)).toBeInTheDocument();
    expect(screen.getByText('keep last 7')).toBeInTheDocument();
    expect(screen.getAllByText('Not configured').length).toBeGreaterThan(0); // S3
  });

  it('database esterno: host, database e sslmode', async () => {
    setup({
      ...base,
      database: {
        mode: 'external',
        host: 'pg.acme.local',
        port: 5432,
        name: 'gitstack',
        sslmode: 'verify-full',
        serverVersion: '16.4',
        versionLive: false,
      },
    });
    expect(await screen.findByText('External')).toBeInTheDocument();
    expect(screen.getByText('pg.acme.local:5432')).toBeInTheDocument();
    expect(screen.getByText('gitstack')).toBeInTheDocument();
    expect(screen.getByText('verify-full')).toBeInTheDocument();
    expect(screen.getByText(/detected at install time/i)).toBeInTheDocument();
  });

  it('backup S3 fallito: errore di caricamento e backup locale valido', async () => {
    setup({
      ...base,
      backup: {
        ...base.backup,
        s3: {
          configured: true,
          endpoint: 'https://s3.acme.local',
          bucket: 'acme-backups',
          prefix: 'gitstack',
          retention: '30',
          upload: { state: 'failed', at: '2026-10-12T02:03:00Z', error: 'timeout' },
        },
      },
    });
    expect(await screen.findByText('s3://acme-backups/gitstack')).toBeInTheDocument();
    const row = screen.getByText(/S3 upload failed/i).closest('.list-row') as HTMLElement;
    expect(within(row).getByText(/timeout · the local backup is valid/i)).toBeInTheDocument();
  });

  it('k3s: aggiornamento di patch automatico e minore con gitstack upgrade --k3s', async () => {
    const { unmount } = setup({
      ...base,
      versions: { ...base.versions, k3sDeclared: 'v1.36.7+k3s1', k3sUpdate: 'patch' },
    });
    expect(await screen.findByText(/k3s patch update available/i)).toBeInTheDocument();
    expect(screen.getByText(/applied automatically/i)).toBeInTheDocument();
    unmount();

    setup({ ...base, versions: { ...base.versions, k3sDeclared: 'v1.37.0+k3s1', k3sUpdate: 'minor' } });
    expect(await screen.findByText(/k3s minor update available/i)).toBeInTheDocument();
    expect(screen.getByText('gitstack upgrade --k3s')).toBeInTheDocument();
  });

  it('rete aziendale: proxy senza credenziali, CA aziendale', async () => {
    setup({
      ...base,
      network: {
        configured: true,
        httpProxy: 'http://proxy.acme.local:3128',
        noProxy: '.acme.local',
        proxyHasCredentials: true,
        corporateCA: true,
        mirrorCA: false,
        registryMirror: 'https://registry.acme.local',
        registryHasCredentials: true,
        downloadMirror: 'https://mirror.acme.local/gitstack',
      },
    });
    expect(await screen.findByText('http://proxy.acme.local:3128')).toBeInTheDocument();
    expect(screen.getByText(/installed in gitstack services/i)).toBeInTheDocument();
    expect(screen.getByText('https://registry.acme.local')).toBeInTheDocument();
    expect(screen.getByText('https://mirror.acme.local/gitstack')).toBeInTheDocument();
    expect(screen.getAllByText(/credentials set \(not shown\)/i)).toHaveLength(2);
  });

  it('informazioni dell host non pubblicate: avviso con il comando', async () => {
    setup({
      ...base,
      published: false,
      database: { mode: 'unknown', serverVersion: '16.4', versionLive: true },
      versions: { gitstack: '', k3sUpdate: 'none' },
    });
    await waitFor(() => expect(screen.getByText(/host information not published/i)).toBeInTheDocument());
    expect(screen.getByText('sudo gitstack system-info publish')).toBeInTheDocument();
  });
});
