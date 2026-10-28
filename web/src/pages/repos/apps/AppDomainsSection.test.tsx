import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../../lib/http';
import { AppAccessPage } from './AppAccessPage';
import { makeApp, repo } from './fixtures';

vi.mock('../../../lib/appsApi', () => ({
  fetchAppAccess: vi.fn(),
  saveAppAccess: vi.fn(),
  fetchAppAccessMembers: vi.fn(),
  fetchAppDomains: vi.fn(),
  addAppDomain: vi.fn(),
  recheckAppDomain: vi.fn(),
  removeAppDomain: vi.fn(),
  uploadAppDomainCertificate: vi.fn(),
  removeAppDomainCertificate: vi.fn(),
}));

import * as api from '../../../lib/appsApi';
import type { AppDomain } from '../../../lib/appsApi';
import { appPermissions } from '../../../lib/appsPermissions';
import type { AppRole } from '../../../lib/appsPermissions';

const T = 20000;
const publicAccess = {
  mode: 'public' as const,
  publicAppsEnabled: true,
  address: 'web-handbook-acme.apps.acme.it',
  certificate: 'lets_encrypt' as const,
  changedAt: '2026-03-12T10:00:00Z',
  changedBy: { id: 'u1', username: 'mrossi', type: 'human' as const },
};
const noCert = { mode: 'letsencrypt' as const, state: 'none' as const };

function domain(over: Partial<AppDomain> & { domain: string }): AppDomain {
  return {
    id: over.domain,
    status: 'pending_dns',
    dns: { type: 'CNAME', name: over.domain, value: 'apps.acme.it', alternatives: [] },
    certificate: noCert,
    createdAt: '2026-10-20T10:00:00Z',
    ...over,
  };
}

const verified = domain({
  domain: 'handbook.acme.it',
  status: 'verified',
  certificate: { mode: 'letsencrypt', state: 'valid', validUntil: '2027-01-10T00:00:00Z' },
});
const waiting = domain({ domain: 'docs.acme.com' });
const broken = domain({ domain: 'wrong.acme.com', status: 'error', statusReason: 'DNS answers 203.0.113.9, expected 198.51.100.4' });

function mount(role: AppRole, access = publicAccess) {
  vi.mocked(api.fetchAppAccess).mockResolvedValue(access);
  vi.mocked(api.fetchAppAccessMembers).mockResolvedValue([]);
  render(
    <MemoryRouter>
      <AppAccessPage repo={repo} app={makeApp({ access: access.mode })} perms={appPermissions(role)} onChange={vi.fn()} />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

// Mockup .prisma/design/mockups-v3-hosting/index.html, data-screen="access", variante «pub».
describe('Domini propri (H7, variante pubblica)', () => {
  it('mostra indirizzo automatico, domini con stato, record DNS e motivo dell’errore', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [verified, waiting, broken] });
    mount('admin');
    const ok = await screen.findByTestId('domain-handbook.acme.it', undefined, { timeout: T });
    expect(within(ok).getByText('DNS points here')).toBeInTheDocument();
    expect(within(ok).getByText(/Let's Encrypt · until/)).toBeInTheDocument();
    const pending = screen.getByTestId('domain-docs.acme.com');
    expect(within(pending).getByText('Waiting for DNS')).toBeInTheDocument();
    expect(within(pending).getByText('CNAME docs.acme.com → apps.acme.it')).toBeInTheDocument();
    const err = screen.getByTestId('domain-wrong.acme.com');
    expect(within(err).getByText('DNS error')).toBeInTheDocument();
    expect(within(err).getByText('DNS answers 203.0.113.9, expected 198.51.100.4')).toBeInTheDocument();
    expect(screen.getByText('web-handbook-acme.apps.acme.it')).toBeInTheDocument();
    expect(screen.getByText(/chosen by/)).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Public on the internet' })).toBeChecked();
    expect(screen.getByText(/port 80 of this server/)).toBeInTheDocument();
  }, T);

  it('certificato in errore: mostra il motivo', async () => {
    const d = domain({ domain: 'shop.acme.it', status: 'verified', certificate: { mode: 'letsencrypt', state: 'error', error: 'port 80 not reachable' } });
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [d] });
    mount('admin');
    const row = await screen.findByTestId('domain-shop.acme.it', undefined, { timeout: T });
    expect(within(row).getByText('Certificate error')).toBeInTheDocument();
    expect(within(row).getByText('port 80 not reachable')).toBeInTheDocument();
  }, T);

  it('admin aggiunge un dominio e vede il record da creare', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [] });
    vi.mocked(api.addAppDomain).mockResolvedValue(waiting);
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: /Add domain/ }, { timeout: T }));
    await userEvent.type(screen.getByLabelText('Domain name'), 'Docs.Acme.com');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(api.addAppDomain).toHaveBeenCalledWith('acme', 'handbook', 'web', 'docs.acme.com'), { timeout: T });
    expect(await screen.findByText('CNAME docs.acme.com → apps.acme.it', undefined, { timeout: T })).toBeInTheDocument();
  }, T);

  it('«Check now» aggiorna lo stato; copia il record', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [waiting] });
    vi.mocked(api.recheckAppDomain).mockResolvedValue({ ...waiting, status: 'verified' });
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: /Copy record/ }, { timeout: T }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('CNAME docs.acme.com → apps.acme.it'), { timeout: T });
    await userEvent.click(screen.getByRole('button', { name: 'Check docs.acme.com now' }));
    await waitFor(() => expect(api.recheckAppDomain).toHaveBeenCalledWith('acme', 'handbook', 'web', 'docs.acme.com'), { timeout: T });
    expect(await screen.findByText('DNS points here', undefined, { timeout: T })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Check docs.acme.com now/ })).not.toBeInTheDocument();
  }, T);

  it('rimozione con conferma', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [waiting] });
    vi.mocked(api.removeAppDomain).mockResolvedValue(undefined);
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: 'Remove docs.acme.com' }, { timeout: T }));
    expect(api.removeAppDomain).not.toHaveBeenCalled();
    await userEvent.click(await screen.findByRole('button', { name: 'Remove domain' }, { timeout: T }));
    await waitFor(() => expect(api.removeAppDomain).toHaveBeenCalledWith('acme', 'handbook', 'web', 'docs.acme.com'), { timeout: T });
    await waitFor(() => expect(screen.queryByTestId('domain-docs.acme.com')).not.toBeInTheDocument(), { timeout: T });
  }, T);

  it('carica il certificato del cliente', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [verified] });
    vi.mocked(api.uploadAppDomainCertificate).mockResolvedValue({ ...verified, certificate: { mode: 'custom', state: 'valid', validUntil: '2027-05-01T00:00:00Z' } });
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: 'Certificate of handbook.acme.it' }, { timeout: T }));
    await userEvent.type(screen.getByLabelText(/Certificate and intermediate chain/), 'CERT');
    await userEvent.type(screen.getByLabelText(/Private key/), 'KEY');
    await userEvent.click(screen.getByRole('button', { name: 'Upload certificate' }));
    await waitFor(() => expect(api.uploadAppDomainCertificate).toHaveBeenCalledWith('acme', 'handbook', 'web', 'handbook.acme.it', 'CERT', 'KEY'), { timeout: T });
    expect(await screen.findByText(/Uploaded certificate · until/, undefined, { timeout: T })).toBeInTheDocument();
  }, T);

  it('errore del contratto (409) con testo chiaro; 422 con il campo', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [] });
    vi.mocked(api.addAppDomain).mockRejectedValueOnce(new ApiError({ error: { code: 'conflict', message: 'Domain already in use.' } }, 409));
    mount('admin');
    await userEvent.click(await screen.findByRole('button', { name: /Add domain/ }, { timeout: T }));
    await userEvent.type(screen.getByLabelText('Domain name'), 'docs.acme.com');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(await screen.findByText(/Domain already in use\. The domain may already be used by another app/, undefined, { timeout: T })).toBeInTheDocument();
    vi.mocked(api.addAppDomain).mockRejectedValueOnce(
      new ApiError({ error: { code: 'validation_failed', message: 'Invalid domain', details: { fields: { domain: 'must not be a wildcard' } } } }, 422),
    );
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(await screen.findByText(/domain: must not be a wildcard/, undefined, { timeout: T })).toBeInTheDocument();
  }, T);

  it.each(['read', 'write'] as const)('%s: vede l’indirizzo automatico, nessun comando e nessuna chiamata ai domini', async (role) => {
    mount(role);
    expect(await screen.findByText('web-handbook-acme.apps.acme.it', undefined, { timeout: T })).toBeInTheDocument();
    expect(screen.getByText(/visible to admins/)).toBeInTheDocument();
    expect(api.fetchAppDomains).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /Add domain/ })).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Public on the internet' })).toBeDisabled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  }, T);
});

describe('Scelta «Public on the internet» (H7)', () => {
  it('chiede conferma prima di salvare', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [] });
    vi.mocked(api.saveAppAccess).mockResolvedValue(publicAccess);
    mount('admin', { ...publicAccess, mode: 'login' as never, changedBy: undefined as never });
    await userEvent.click(await screen.findByRole('radio', { name: 'Public on the internet' }, { timeout: T }));
    expect(api.saveAppAccess).not.toHaveBeenCalled();
    await userEvent.click(await screen.findByRole('button', { name: 'Make public' }, { timeout: T }));
    await waitFor(() => expect(api.saveAppAccess).toHaveBeenCalledWith('acme', 'handbook', 'web', 'public'), { timeout: T });
  }, T);

  it('annullando non salva', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [] });
    mount('admin', { ...publicAccess, mode: 'login' as never });
    await userEvent.click(await screen.findByRole('radio', { name: 'Public on the internet' }, { timeout: T }));
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }, { timeout: T }));
    expect(api.saveAppAccess).not.toHaveBeenCalled();
  }, T);

  it('interruttore spento: disabilitata con la nota e senza «Add domain»', async () => {
    vi.mocked(api.fetchAppDomains).mockResolvedValue({ items: [] });
    mount('admin', { ...publicAccess, mode: 'login' as never, publicAppsEnabled: false });
    expect(await screen.findByRole('radio', { name: 'Public on the internet' }, { timeout: T })).toBeDisabled();
    expect(screen.getByText('disabled by the administrator')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add domain/ })).not.toBeInTheDocument();
  }, T);
});
