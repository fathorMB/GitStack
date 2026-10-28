import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppsPage } from './AppsPage';
import { repo, makeApp, wiki, worker } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({ fetchApps: vi.fn() }));
vi.mock('../../../lib/useAppPermissions', () => ({ useAppPermissions: vi.fn() }));

import { fetchApps } from '../../../lib/appsApi';
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
    const shop = makeApp({ id: 'a4', name: 'shop', access: 'public', address: 'shop-handbook-acme.apps.acme.it', publicAddress: 'shop-handbook-acme.apps.acme.it' });
    const shopVerified = makeApp({ id: 'a5', name: 'shop', access: 'public', address: 'shop-handbook-acme.apps.acme.it', publicAddress: 'https://shop.retail.it' });

    it('app interna: badge con login e indirizzo automatico, nessun Public', async () => {
      setup('admin', [makeApp()]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Internal · GitStack login');
      expect(row).not.toHaveTextContent('Public');
      expect(row).toHaveTextContent('web-handbook-acme.apps.acme.it');
    }, T);

    it('app pubblica senza domini propri: badge Public e indirizzo automatico', async () => {
      setup('admin', [shop]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Public');
      expect(row).toHaveTextContent('shop-handbook-acme.apps.acme.it');
    }, T);

    it.each(['read', 'write', 'admin'] as const)('con %s la lista mostra il dominio verificato di publicAddress, senza chiamate ai domini', async (role) => {
      setup(role, [shopVerified]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      const row = list.querySelector('li') as HTMLElement;
      expect(row).toHaveTextContent('Public');
      expect(row).toHaveTextContent('https://shop.retail.it');
      expect(row).not.toHaveTextContent('shop-handbook-acme.apps.acme.it');
    }, T);

    it('senza publicAddress ricade sull indirizzo automatico', async () => {
      setup('read', [makeApp({ id: 'a6', name: 'old', access: 'public', address: 'old-handbook-acme.apps.acme.it', publicAddress: null })]);
      const list = await screen.findByRole('list', { name: 'Apps' }, { timeout: T });
      expect(list.querySelector('li')).toHaveTextContent('old-handbook-acme.apps.acme.it');
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
