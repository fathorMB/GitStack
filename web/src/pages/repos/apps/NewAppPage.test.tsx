import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../../lib/http';
import type { Package } from '../../../lib/packagesApi';
import { NewAppPage } from './NewAppPage';
import { repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({ createApp: vi.fn() }));
vi.mock('../../../lib/packagesApi', () => ({ fetchPackages: vi.fn() }));
vi.mock('../../../lib/useAppPermissions', () => ({ useAppPermissions: vi.fn() }));

import { createApp } from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import { fetchPackages } from '../../../lib/packagesApi';
import { useAppPermissions } from '../../../lib/useAppPermissions';

const T = 20000;
const pkg: Package = {
  name: 'acme/handbook/web',
  tags: ['sha-7f3a91c'],
  sizeBytes: 1,
  updatedAt: '2026-10-25T10:00:00Z',
  signature: 'verified',
  hasSbom: false,
  kind: 'image',
  tagDetails: [{ name: 'sha-7f3a91c', digest: 'sha256:ab', sizeBytes: 1, pushedAt: '2026-10-25T10:00:00Z', signature: 'verified', hasSbom: false, protected: false }],
};

function setup(role: 'read' | 'write' | 'admin' = 'admin', items: Package[] = [pkg]) {
  vi.mocked(useAppPermissions).mockReturnValue({ ...appPermissions(role), ready: true });
  vi.mocked(fetchPackages).mockResolvedValue({ items });
  render(
    <MemoryRouter initialEntries={['/acme/handbook/apps/new']}>
      <Routes>
        <Route path="/acme/handbook/apps/new" element={<NewAppPage repo={repo} />} />
        <Route path="/acme/handbook/apps/:app" element={<div>overview-page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="new".
describe('NewAppPage (H2)', () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('immagine del repo firmata: banner firmata, segue il branch, crea e apre l overview', async () => {
    setup();
    expect(await screen.findByText(/Signed by the CI of/, {}, { timeout: T })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Follow branch main/ })).toBeChecked();
    vi.mocked(createApp).mockResolvedValue({ app: { name: 'web' }, version: {} } as never);
    await userEvent.type(screen.getByLabelText('Name'), 'web');
    await userEvent.click(screen.getByRole('button', { name: /Create and deploy/ }));
    await screen.findByText('overview-page', {}, { timeout: T });
    const call = vi.mocked(createApp).mock.calls[0];
    expect(call?.[2]).toMatchObject({ name: 'web', image: expect.stringContaining('/acme/handbook/web:sha-7f3a91c'), followBranch: 'main', port: 8080, copies: 2 });
    expect(call?.[2].resources?.guaranteed).toEqual({ cpuMillis: 500, memoryBytes: 512 * 1024 * 1024 });
  }, T);

  it('immagine esterna: etichetta non verificata, niente Follow branch, deploy manuale', async () => {
    setup();
    await screen.findByText(/Signed by the CI/, {}, { timeout: T });
    await userEvent.click(screen.getByRole('button', { name: 'External registry' }));
    expect(screen.getByText('Not verified.')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Follow branch/ })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /Manual only/ })).toBeChecked();
    vi.mocked(createApp).mockResolvedValue({ app: { name: 'wiki' }, version: {} } as never);
    await userEvent.type(screen.getByLabelText('Name'), 'wiki');
    await userEvent.type(screen.getByLabelText('External image'), 'docker.io/requarks/wiki:2.5');
    await userEvent.click(screen.getByRole('button', { name: /Create and deploy/ }));
    await waitFor(() => expect(createApp).toHaveBeenCalled(), { timeout: T });
    expect(vi.mocked(createApp).mock.calls[0]?.[2]).toMatchObject({ image: 'docker.io/requarks/wiki:2.5', followBranch: null });
  }, T);

  it('server pieno: rifiuto chiaro e pulsante spento finche non si modifica qualcosa', async () => {
    setup();
    await screen.findByText(/Signed by the CI/, {}, { timeout: T });
    vi.mocked(createApp).mockRejectedValue(new ApiError({ error: { code: 'insufficient_capacity', message: 'Missing 0.6 vCPU.' } }, 409));
    await userEvent.type(screen.getByLabelText('Name'), 'web');
    await userEvent.click(screen.getByRole('button', { name: /Create and deploy/ }));
    expect(await screen.findByText('Not enough room on the server.', {}, { timeout: T })).toBeInTheDocument();
    expect(screen.getByText(/Missing 0.6 vCPU/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create and deploy/ })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: 'Fewer copies' }));
    expect(screen.queryByText('Not enough room on the server.')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create and deploy/ })).toBeEnabled();
  }, T);

  it.each(['read', 'write'] as const)('con %s la pagina non offre il modulo', async (role) => {
    setup(role);
    expect(await screen.findByText(/requires admin/, {}, { timeout: T })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Create and deploy/ })).not.toBeInTheDocument();
  }, T);
});
