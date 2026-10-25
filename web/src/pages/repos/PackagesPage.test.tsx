import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { Package } from '../../lib/packagesApi';
import type { Repository } from '../../lib/reposApi';
import { PackagesPage } from './PackagesPage';

vi.mock('../../lib/packagesApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/packagesApi')>()),
  fetchPackages: vi.fn(),
  deletePackage: vi.fn(),
}));

import { deletePackage, fetchPackages } from '../../lib/packagesApi';

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', visibility: 'private', archived: false } as Repository;
const T = 20000;

const digest = 'sha256:' + 'ab'.repeat(32);

const pkgs: Package[] = [
  {
    name: 'acme/api',
    tags: ['v1.2.0', 'debug-mrossi'],
    sizeBytes: 52428800,
    updatedAt: '2026-10-01T10:00:00Z',
    digest,
    signature: 'verified',
    hasSbom: true,
    kind: 'image',
    pushedBy: { kind: 'job', login: 'ci job token' },
    tagDetails: [
      { name: 'v1.2.0', digest, sizeBytes: 52428800, pushedAt: '2026-10-01T10:00:00Z', signature: 'verified', hasSbom: true, protected: true },
      { name: 'debug-mrossi', digest: 'sha256:' + 'cd'.repeat(32), sizeBytes: 1048576, pushedAt: '2026-09-01T10:00:00Z', signature: 'unverified', hasSbom: false, protected: false },
    ],
  },
  { name: 'acme/api/charts/api', tags: ['1.0.0'], sizeBytes: 1024, updatedAt: '2026-10-02T10:00:00Z', signature: 'unverified', hasSbom: false, kind: 'helm' },
  { name: 'acme/api/worker', tags: [], sizeBytes: 1024, updatedAt: '2026-10-02T10:00:00Z', signature: 'unverified', hasSbom: false },
];

function setup(admin: boolean, items: Package[] = pkgs) {
  vi.mocked(fetchPackages).mockResolvedValue({ items });
  render(
    <ToastProvider>
      <MemoryRouter>
        <PackagesPage repo={repo} admin={admin} />
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('PackagesPage (mockup B5)', () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('elenco Images: Latest, numero di tag, dimensione, chi ha pubblicato, visibilità, Helm', async () => {
    setup(false);
    const row = await screen.findByRole('listitem', { name: 'Package acme/api' }, { timeout: 5000 });
    expect(screen.getByText('Visibility follows the repository: private')).toBeInTheDocument();
    expect(row).toHaveTextContent('Latest v1.2.0 · 2 tags · 50 MB');
    expect(row).toHaveTextContent('by ci job token');
    const helm = screen.getByRole('listitem', { name: 'Package acme/api/charts/api' });
    expect(within(helm).getByText('Helm')).toBeInTheDocument();
    expect(helm).toHaveTextContent('1 version');
    expect(helm).not.toHaveTextContent(' by ');
    expect(within(row).queryByText('Helm')).toBeNull();
  }, T);

  it('card dell\'immagine scelta: una riga per tag con i badge e digest · dimensione', async () => {
    setup(false);
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    const prot = screen.getByRole('listitem', { name: 'Tag v1.2.0' });
    expect(within(prot).getByText('protected')).toBeInTheDocument();
    expect(within(prot).getByText('signed by CI')).toBeInTheDocument();
    expect(within(prot).getByText('SBOM')).toBeInTheDocument();
    expect(prot).toHaveTextContent('sha256:abab…ab · 50 MB');
    const hand = screen.getByRole('listitem', { name: 'Tag debug-mrossi' });
    expect(within(hand).getByText('not verified')).toBeInTheDocument();
    expect(within(hand).getByText('pushed by hand with docker push · no signature')).toBeInTheDocument();
    expect(within(hand).queryByText('SBOM')).toBeNull();
    expect(within(hand).queryByText('protected')).toBeNull();
    // Dato non nel contratto: niente «used by app».
    expect(screen.queryByText(/used by app ·/)).toBeNull();
  }, T);

  it('cliccando un\'altra immagine la card e il comando di pull cambiano', async () => {
    setup(false);
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    await userEvent.click(screen.getByRole('button', { name: 'Select acme/api/charts/api' }));
    expect(screen.getByRole('region', { name: 'Tags of acme/api/charts/api' })).toBeInTheDocument();
    expect(screen.getByLabelText('Pull command for acme/api/charts/api')).toHaveTextContent(`docker pull ${window.location.host}/acme/api/charts/api:1.0.0`);
  }, T);

  it('card Pull: login e pull dell\'ultimo tag, con aiuto e i due alert', async () => {
    setup(false);
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    const cmd = screen.getByLabelText('Pull command for acme/api');
    expect(cmd).toHaveTextContent(`docker login ${window.location.host}`);
    expect(cmd).toHaveTextContent(`docker pull ${window.location.host}/acme/api:v1.2.0`);
    expect(screen.getByText(/Log in with a personal or agent token with the/)).toHaveTextContent('Inside workflows the job token works.');
    expect(screen.getByText(/signs every image it publishes/)).toBeInTheDocument();
    expect(screen.getByText(/not in backups/)).toBeInTheDocument();
  }, T);

  it('senza admin non offre né l\'eliminazione né Cleanup rules', async () => {
    setup(false);
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    expect(screen.queryByRole('button', { name: /^Delete/ })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Cleanup rules' })).toBeNull();
  }, T);

  it('admin: Cleanup rules porta a B6 ed elimina un tag dopo la conferma', async () => {
    setup(true);
    vi.mocked(deletePackage).mockResolvedValue();
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    expect(screen.getByRole('link', { name: 'Cleanup rules' })).toHaveAttribute('href', '/acme/api/settings/packages');
    await userEvent.click(screen.getByRole('button', { name: 'Delete tag debug-mrossi of acme/api' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(deletePackage).toHaveBeenCalledWith('acme', 'api', 'acme/api', 'debug-mrossi'));
  }, T);

  it('admin: l\'immagine intera si elimina riscrivendo il nome', async () => {
    setup(true);
    vi.mocked(deletePackage).mockResolvedValue();
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    await userEvent.click(screen.getByRole('button', { name: 'Select acme/api/worker' }));
    await userEvent.click(screen.getByRole('button', { name: 'Delete acme/api/worker' }));
    const confirm = await screen.findByRole('button', { name: 'Delete' });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByPlaceholderText('acme/api/worker'), 'acme/api/worker');
    await userEvent.click(confirm);
    await waitFor(() => expect(deletePackage).toHaveBeenCalledWith('acme', 'api', 'acme/api/worker', undefined));
  }, T);

  it('mostra l\'errore se l\'eliminazione fallisce (tag protetto)', async () => {
    setup(true);
    const { ApiError } = await import('../../lib/http');
    vi.mocked(deletePackage).mockRejectedValue(new ApiError({ error: { code: 'conflict', message: 'Tag is protected' } }, 409));
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    await userEvent.click(screen.getByRole('button', { name: 'Delete tag v1.2.0 of acme/api' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/protected/i);
  }, T);

  it('senza immagini mostra lo stato vuoto', async () => {
    setup(false, []);
    expect(await screen.findByText('No packages yet')).toBeInTheDocument();
  }, T);
});
