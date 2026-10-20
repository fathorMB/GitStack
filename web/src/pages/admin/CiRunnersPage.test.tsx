import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { CiRunnersPage } from './CiRunnersPage';

vi.mock('../../lib/authApi', () => ({ fetchSession: vi.fn() }));
vi.mock('../../lib/ciApi', () => ({
  fetchCiRunners: vi.fn(),
  fetchCiQueue: vi.fn(),
  createCiRegistrationToken: vi.fn(),
  deleteCiRunner: vi.fn(),
}));

import { fetchSession } from '../../lib/authApi';
import { createCiRegistrationToken, deleteCiRunner, fetchCiQueue, fetchCiRunners } from '../../lib/ciApi';

const queue = {
  queuedJobs: 2,
  runningJobs: 1,
  maxConcurrentJobs: 3,
  resourceLimits: {
    cpuCores: 2,
    memoryBytes: 4294967296,
    ephemeralStorageBytes: 21474836480,
    maxDurationSeconds: 7200,
  },
};

const runners = [
  { id: 'r1', name: 'build-01', labels: ['linux-x64', 'large'], status: 'online' as const },
  { id: 'r2', name: 'mac-mini', labels: ['macos-arm64'], status: 'offline' as const },
  { id: 'r3', name: 'win-ci', labels: ['windows-x64'], status: 'busy' as const },
];

async function card(name: string): Promise<HTMLElement> {
  const el = await screen.findByText(name);
  return el.closest('.card') as HTMLElement;
}

async function stat(label: string): Promise<HTMLElement> {
  const el = await screen.findByText(label);
  return el.closest('.stat') as HTMLElement;
}

function renderPage() {
  return render(
    <ToastProvider>
      <CiRunnersPage />
    </ToastProvider>,
  );
}

describe('Admin · CI & runners (mockup B4)', { timeout: 30000 }, () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'mrossi', isAdmin: true } } as never);
    vi.mocked(fetchCiRunners).mockResolvedValue({ items: runners });
    vi.mocked(fetchCiQueue).mockResolvedValue(queue);
  });

  it('coda, posti e limiti vengono dal contratto, con i valori del profilo', async () => {
    renderPage();
    expect((await stat('Built-in slots in use')).querySelector('b')?.textContent).toBe('1 / 3');
    expect((await stat('Jobs waiting · max half of the slots per owner')).querySelector('b')?.textContent).toBe('2');
    expect((await stat('External runners online')).querySelector('b')?.textContent).toBe('2');
    expect((await stat('Per job · 4.0 GB RAM · 20 GB disk · 2 h')).querySelector('b')?.textContent).toBe('2 vCPU');
  });

  it('runner esterni con etichette e stato, e revoca che chiama il DELETE', async () => {
    renderPage();
    const box = await card('External runners');
    const row = within(box).getByText('build-01');
    expect(within(box).getAllByText('large').length).toBe(1);
    expect(row.textContent).toBe('build-01');
    await userEvent.click(await within(box).findByRole('button', { name: 'Revoke build-01' }));
    const dialog = await screen.findByRole('dialog');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(deleteCiRunner).toHaveBeenCalledWith('r1'));
  });

  it('il token di registrazione: si legge solo la scadenza, mai il valore', async () => {
    vi.mocked(createCiRegistrationToken).mockResolvedValue({
      expiresAt: '2026-11-01T00:00:00Z',
      token: 'reg-token-value',
    } as never);
    renderPage();
    await userEvent.click(await screen.findByRole('button', { name: 'Register external runner' }));
    await waitFor(() => expect(createCiRegistrationToken).toHaveBeenCalled());

    const box = await card('Registration token');
    expect(within(box).getByText(/Valid until/)).toBeInTheDocument();
    expect(within(box).getByText(/never reads it back/)).toBeInTheDocument();
    // Il contratto ha `token` come writeOnly: la UI non puo' rileggerlo.
    expect(within(box).queryByText('reg-token-value')).toBeNull();
  });

  it('spazio di log, artefatti e cache con le regole CI5, senza numeri inventati', async () => {
    renderPage();
    const box = await card('Storage');
    expect(within(box).getByText(/kept 90 days · in backups/)).toBeInTheDocument();
    expect(within(box).getByText(/images used by apps always included/)).toBeInTheDocument();
    expect(within(box).getByText(/Space used is not exposed by the API yet/)).toBeInTheDocument();
  });

  it('non amministratore: la console non carica niente', async () => {
    vi.mocked(fetchSession).mockResolvedValue({ user: { username: 'lbianchi', isAdmin: false } } as never);
    renderPage();
    expect(await screen.findByText('Only installation administrators can see CI and runners.')).toBeInTheDocument();
    expect(fetchCiRunners).not.toHaveBeenCalled();
    expect(screen.queryByText('Storage')).toBeNull();
  });
});
