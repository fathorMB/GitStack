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
  access: { host: 'git.acme.local', tlsMode: 'internal', tlsExpiresAt: '2099-01-01T00:00:00Z', sshPort: 2222 },
  services: {
    available: true,
    items: [
      { name: 'gs-core', kind: 'Deployment', desired: 2, ready: 2, image: 'reg/core:v1.0.0', healthy: true },
      { name: 'gs-git', kind: 'StatefulSet', desired: 1, ready: 0, image: 'reg/git:v1.0.0', healthy: false },
    ],
  },
  node: {
    available: true,
    cpu: { available: true, value: 8, minimum: 4, recommended: 8, level: 'recommended' },
    memory: { available: true, value: 12_000_000_000, minimum: 8_000_000_000, recommended: 16_000_000_000, level: 'below_recommended' },
    disk: {
      available: true,
      value: 50_000_000_000,
      minimum: 60_000_000_000,
      recommended: 200_000_000_000,
      level: 'below_minimum',
      path: '/var/lib/rancher',
      freeBytes: 30_000_000_000,
      minFreeBytes: 20_000_000_000,
    },
  },
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

  it('avviso della catena di fornitura: in evidenza in cima quando presente', async () => {
    setup({ ...base, supplyChainWarning: 'Firme non verificate: mirror interno non firmato (v5.allow_unsigned=true).' });
    const alert = await screen.findByTestId('supply-chain-warning');
    expect(alert).toHaveAttribute('role', 'alert');
    expect(within(alert).getByText(/mirror interno non firmato/i)).toBeInTheDocument();
    // in cima: prima di tutte le carte
    const card = screen.getByText('Access').closest('.card') as HTMLElement;
    expect(alert.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('avviso della catena di fornitura: assente quando non c e', async () => {
    setup(base);
    await screen.findByText('Internal (bundled)');
    expect(screen.queryByTestId('supply-chain-warning')).not.toBeInTheDocument();
    expect(screen.queryByText(/supply-chain warning/i)).not.toBeInTheDocument();
  });

  it('Access: host, modo HTTPS, scadenza del certificato e porta SSH', async () => {
    setup(base);
    const card = (await screen.findByText('Access')).closest('.card') as HTMLElement;
    expect(within(card).getByText('git.acme.local')).toBeInTheDocument();
    expect(within(card).getByText('Internal CA')).toBeInTheDocument();
    expect(within(card).getByText(/in \d+ days/)).toBeInTheDocument();
    expect(within(card).getByText('2222')).toBeInTheDocument();
  });

  it('Access: certificato scaduto, HTTP senza certificato, scadenza non disponibile', async () => {
    const { unmount } = setup({ ...base, access: { ...base.access, tlsExpiresAt: '2020-01-01T00:00:00Z' } });
    expect(await screen.findByText(/^Expired /)).toBeInTheDocument();
    unmount();
    const second = setup({ ...base, access: { host: '10.0.0.5', tlsMode: 'insecure', sshPort: 2222 } });
    expect(await screen.findByText(/no certificate \(http\)/i)).toBeInTheDocument();
    second.unmount();
    setup({ ...base, access: { ...base.access, tlsExpiresAt: null } });
    const card = (await screen.findByText('Access')).closest('.card') as HTMLElement;
    expect(within(card).getByText('Not available')).toBeInTheDocument();
  });

  it('Services: stato e repliche di ogni deployment con la data di pubblicazione', async () => {
    setup(base);
    const card = (await screen.findByText('Services')).closest('.card') as HTMLElement;
    const core = within(card).getByText('gs-core').closest('.list-row') as HTMLElement;
    expect(within(core).getByText('Ready')).toBeInTheDocument();
    expect(within(core).getByText('2/2')).toBeInTheDocument();
    const git = within(card).getByText('gs-git').closest('.list-row') as HTMLElement;
    expect(within(git).getByText('Not ready')).toBeInTheDocument();
    expect(within(git).getByText('0/1')).toBeInTheDocument();
    expect(within(card).getByText(/^as of /)).toBeInTheDocument();
  });

  it('Services: cluster non interrogabile', async () => {
    setup({ ...base, services: { available: false, error: 'cluster non interrogabile', items: [] } });
    const card = (await screen.findByText('Services')).closest('.card') as HTMLElement;
    expect(within(card).getByText(/not available: cluster non interrogabile/i)).toBeInTheDocument();
  });

  it('Node: CPU, memoria e disco confrontati con il profilo consigliato', async () => {
    setup(base);
    const card = (await screen.findByText('Node')).closest('.card') as HTMLElement;
    const cpu = within(card).getByText('CPU').closest('.list-row') as HTMLElement;
    expect(within(cpu).getByText('8 vCPU')).toBeInTheDocument();
    expect(within(cpu).getByText(/meets the recommended profile/i)).toBeInTheDocument();
    const mem = within(card).getByText('Memory').closest('.list-row') as HTMLElement;
    expect(within(mem).getByText('12.0 GB')).toBeInTheDocument();
    expect(within(mem).getByText(/below the recommended profile \(minimum 8.0 GB, recommended 16.0 GB\)/i)).toBeInTheDocument();
    const disk = within(card).getByText('Disk').closest('.list-row') as HTMLElement;
    expect(within(disk).getByText(/below the minimum profile/i)).toBeInTheDocument();
    expect(within(disk).getByText(/30\.0 GB free on \/var\/lib\/rancher/)).toBeInTheDocument();
  });

  it('Node: non disponibile', async () => {
    setup({
      ...base,
      node: {
        available: false,
        cpu: { available: false, value: 0, minimum: 4, recommended: 8, level: 'unknown' },
        memory: { available: false, value: 0, minimum: 8_000_000_000, recommended: 16_000_000_000, level: 'unknown' },
        disk: { available: false, value: 0, minimum: 60_000_000_000, recommended: 200_000_000_000, level: 'unknown', freeBytes: 0, minFreeBytes: 20_000_000_000 },
      },
    });
    const card = (await screen.findByText('Node')).closest('.card') as HTMLElement;
    expect(within(card).getByText(/not available/i)).toBeInTheDocument();
  });
});
