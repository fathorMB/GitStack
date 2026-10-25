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

const repo = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', archived: false } as Repository;

const pkgs: Package[] = [
  { name: 'acme/api', tags: ['v1.2.0', 'latest'], sizeBytes: 52428800, updatedAt: '2026-10-01T10:00:00Z', digest: 'sha256:' + 'ab'.repeat(32), signature: 'verified', hasSbom: true },
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

  it('mostra immagini, tag, firma, SBOM e comando di pull', async () => {
    setup(false);
    const card = await screen.findByRole('listitem', { name: 'Package acme/api' }, { timeout: 5000 });
    expect(within(card).getByText('Signature verified')).toBeInTheDocument();
    expect(within(card).getByText('SBOM')).toBeInTheDocument();
    expect(within(card).getByRole('list', { name: 'Tags of acme/api' })).toHaveTextContent('v1.2.0');
    expect(within(card).getByLabelText('Pull command for acme/api')).toHaveTextContent(`docker pull ${window.location.host}/acme/api:v1.2.0`);

    const other = screen.getByRole('listitem', { name: 'Package acme/api/worker' });
    expect(within(other).getByText('Signature not verified')).toBeInTheDocument();
    expect(within(other).getByText('No SBOM')).toBeInTheDocument();
    expect(within(other).getByText(/Untagged image/)).toBeInTheDocument();
  });

  it('senza admin non offre né l\'eliminazione né le impostazioni', async () => {
    setup(false);
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    expect(screen.queryByRole('button', { name: /^Delete/ })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Package settings' })).toBeNull();
  });

  it('admin: elimina un tag dopo la conferma', async () => {
    setup(true);
    vi.mocked(deletePackage).mockResolvedValue();
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    expect(screen.getByRole('link', { name: 'Package settings' })).toHaveAttribute('href', '/acme/api/settings/packages');
    await userEvent.click(screen.getByRole('button', { name: 'Delete tag latest of acme/api' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(deletePackage).toHaveBeenCalledWith('acme', 'api', 'acme/api', 'latest'));
  });

  it('admin: l\'immagine intera si elimina riscrivendo il nome', async () => {
    setup(true);
    vi.mocked(deletePackage).mockResolvedValue();
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    await userEvent.click(screen.getByRole('button', { name: 'Delete acme/api/worker' }));
    const confirm = await screen.findByRole('button', { name: 'Delete' });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByPlaceholderText('acme/api/worker'), 'acme/api/worker');
    await userEvent.click(confirm);
    await waitFor(() => expect(deletePackage).toHaveBeenCalledWith('acme', 'api', 'acme/api/worker', undefined));
  });

  it('mostra l\'errore se l\'eliminazione fallisce (tag protetto)', async () => {
    setup(true);
    const { ApiError } = await import('../../lib/http');
    vi.mocked(deletePackage).mockRejectedValue(new ApiError({ error: { code: 'conflict', message: 'Tag is protected' } }, 409));
    await screen.findByRole('listitem', { name: 'Package acme/api' });
    await userEvent.click(screen.getByRole('button', { name: 'Delete tag v1.2.0 of acme/api' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/protected/i);
  });

  it('senza immagini mostra lo stato vuoto', async () => {
    setup(false, []);
    expect(await screen.findByText('No packages yet')).toBeInTheDocument();
  });
});
