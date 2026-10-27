import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppsPage } from './AppsPage';
import { repo, makeApp, wiki, worker } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({ fetchApps: vi.fn(), fetchAppDomains: vi.fn() }));
vi.mock('../../../lib/useAppPermissions', () => ({ useAppPermissions: vi.fn() }));

import { fetchAppDomains, fetchApps } from '../../../lib/appsApi';
import type { AppDomain } from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import { useAppPermissions } from '../../../lib/useAppPermissions';

const T = 20000;

function setup(role: 'read' | 'write' | 'admin', items = [makeApp(), worker, wiki]) {
  vi.mocked(useAppPermissions).mockReturnValue({ ...appPermissions(role), ready: true });
  vi.mocked(fetchApps).mockResolvedValue({ items });
  render(
    <MemoryRouter>
      <AppsPage repo={repo} />
    </MemoryRouter>,
  );
}

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="apps".
describe('AppsPage (H1)', () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('elenco: stato, indirizzo, accesso, versione, branch, copie, verificata o no', async () => {
    setup('admin');
    const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
    expect(screen.getByText('3 apps')).toBeInTheDocument();
    const rows = list.querySelectorAll('li');
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent('web-handbook-acme.apps.acme.it');
    expect(rows[0]).toHaveTextContent('follows main');
    expect(rows[0]).toHaveTextContent('v42');
    expect(rows[0]).toHaveTextContent('2 copies');
    expect(rows[0]).toHaveTextContent('Internal · GitStack login');
    expect(rows[0]).toHaveTextContent('verified');
    expect(rows[0]).toHaveTextContent('Running');
    expect(rows[1]).toHaveTextContent('No address');
    expect(rows[1]).toHaveTextContent('1 copy');
    expect(rows[2]).toHaveTextContent('manual deploys');
    expect(rows[2]).toHaveTextContent('docker.io/requarks/wiki:2.5');
    expect(rows[2]).toHaveTextContent('runs as root');
    expect(rows[2]).toHaveTextContent('not verified');
    expect(rows[2]).toHaveTextContent('Restarting');
    expect(screen.getByRole('link', { name: 'web' })).toHaveAttribute('href', '/acme/handbook/apps/web');
  }, T);

  it('il filtro restringe l elenco', async () => {
    setup('write');
    await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
    await userEvent.type(screen.getByLabelText('Filter apps'), 'wik');
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByRole('link', { name: 'wiki' })).toBeInTheDocument();
  }, T);

  describe('v3-c: badge Public e indirizzo nella lista', () => {
    const shop = makeApp({ id: 'a4', name: 'shop', access: 'public', address: 'shop-handbook-acme.apps.acme.it' });
    const domain = (over: Partial<AppDomain>): AppDomain => ({
      id: 'd1',
      domain: 'shop.retail.it',
      status: 'verified',
      dns: { type: 'CNAME', name: 'shop.retail.it', value: 'shop-handbook-acme.apps.acme.it', alternatives: [] },
      certificate: { mode: 'letsencrypt', state: 'valid' },
      createdAt: '2026-10-20T10:00:00Z',
      ...over,
    });

    it('app interna: badge con login e indirizzo automatico, nessun Public, nessuna lettura dei domini', async () => {
      setup('admin', [makeApp()]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Internal · GitStack login');
      expect(row).not.toHaveTextContent('Public');
      expect(row).toHaveTextContent('web-handbook-acme.apps.acme.it');
      expect(fetchAppDomains).not.toHaveBeenCalled();
    }, T);

    it('app pubblica senza domini propri: badge Public e indirizzo automatico', async () => {
      vi.mocked(fetchAppDomains).mockResolvedValue({ items: [] });
      setup('admin', [shop]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Public');
      expect(row).toHaveTextContent('shop-handbook-acme.apps.acme.it');
      await waitFor(() => expect(fetchAppDomains).toHaveBeenCalledWith('acme', 'handbook', 'shop'));
      expect(row).toHaveTextContent('shop-handbook-acme.apps.acme.it');
    }, T);

    it('app pubblica con dominio proprio verificato: mostra il dominio, non quello automatico', async () => {
      vi.mocked(fetchAppDomains).mockResolvedValue({
        items: [domain({ domain: 'pending.retail.it', status: 'pending_dns' }), domain({})],
      });
      setup('admin', [shop]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = await waitFor(() => {
        const r = list.querySelector('li') as HTMLElement;
        expect(r).toHaveTextContent('shop.retail.it');
        return r;
      }, { timeout: T });
      expect(row).toHaveTextContent('Public');
      expect(row).not.toHaveTextContent('shop-handbook-acme.apps.acme.it');
      expect(row).not.toHaveTextContent('pending.retail.it');
    }, T);

    it('senza admin non si chiedono i domini (403) e resta l indirizzo automatico', async () => {
      setup('write', [shop]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Public');
      expect(row).toHaveTextContent('shop-handbook-acme.apps.acme.it');
      expect(fetchAppDomains).not.toHaveBeenCalled();
    }, T);
  });

  it('New app solo con admin', async () => {
    setup('admin');
    expect(await screen.findByRole('link', { name: /New app/ }, { timeout: T })).toHaveAttribute('href', '/acme/handbook/apps/new');
  }, T);

  it.each(['read', 'write'] as const)('con %s non c e New app', async (role) => {
    setup(role);
    await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
    expect(screen.queryByRole('link', { name: /New app/ })).not.toBeInTheDocument();
  }, T);

  it('variante senza app: invito, New app per admin e comando gs', async () => {
    setup('admin', []);
    expect(await screen.findByText('Run this repository as an app', {}, { timeout: T })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /New app/ })).toBeInTheDocument();
    expect(screen.getByText('gs app create')).toBeInTheDocument();
  }, T);

  it('variante senza app, senza admin: nessun pulsante New app e la spiegazione', async () => {
    setup('write', []);
    await screen.findByText('Run this repository as an app', {}, { timeout: T });
    expect(screen.queryByRole('link', { name: /New app/ })).not.toBeInTheDocument();
    expect(screen.getByText(/requires admin/)).toBeInTheDocument();
  }, T);
});
