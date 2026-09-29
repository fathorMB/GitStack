import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { OrgsPage } from './OrgsPage';

vi.mock('../../lib/orgsApi', () => ({ fetchOrganizations: vi.fn(), createOrg: vi.fn() }));

import { createOrg, fetchOrganizations } from '../../lib/orgsApi';

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/orgs']}>
        <Routes>
          <Route path="/orgs" element={<OrgsPage />} />
          <Route path="/orgs/:org" element={<p>org page</p>} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

describe('OrgsPage', () => {
  beforeEach(() => {
    vi.mocked(fetchOrganizations)
      .mockReset()
      .mockResolvedValue([{ id: 'o1', name: 'acme', displayName: 'Acme Inc.', createdAt: '2026-01-01T00:00:00Z' }]);
    vi.mocked(createOrg).mockReset();
  });

  it('elenca le organizzazioni', async () => {
    renderPage();
    expect(await screen.findByRole('link', { name: 'Acme Inc.' })).toHaveAttribute('href', '/orgs/acme');
  });

  it("crea un'organizzazione e apre la sua pagina", async () => {
    vi.mocked(createOrg).mockResolvedValue({ id: 'o2', name: 'newco', createdAt: '2026-01-01T00:00:00Z' });
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('button', { name: /new organization/i }));
    const form = screen.getByRole('form', { name: 'New organization' });
    await u.type(within(form).getByLabelText('Name'), 'newco');
    await u.click(within(form).getByRole('button', { name: 'Create organization' }));
    await waitFor(() => expect(createOrg).toHaveBeenCalledWith({ name: 'newco' }));
    expect(await screen.findByText('org page')).toBeInTheDocument();
  });

  it('422: errore sul campo; 403: messaggio di permessi', async () => {
    vi.mocked(createOrg)
      .mockRejectedValueOnce(
        new ApiError({ error: { code: 'validation_failed', message: 'Invalid', details: { fields: { name: 'already taken' } } } }, 422),
      )
      .mockRejectedValueOnce(new ApiError({ error: { code: 'forbidden', message: 'Not allowed.' } }, 403));
    const u = userEvent.setup();
    renderPage();
    await u.click(await screen.findByRole('button', { name: /new organization/i }));
    const form = screen.getByRole('form', { name: 'New organization' });
    await u.type(within(form).getByLabelText('Name'), 'acme');
    await u.click(within(form).getByRole('button', { name: 'Create organization' }));
    expect(await within(form).findByText('already taken')).toBeInTheDocument();
    await u.click(within(form).getByRole('button', { name: 'Create organization' }));
    expect(await within(form).findByText(/don't have permission/)).toBeInTheDocument();
  });
});
