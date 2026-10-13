import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Outlet, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import type { User } from '../../lib/authApi';
import { ApiError } from '../../lib/http';
import { AuthProvider } from '../../routes/auth';
import { SecurityPage } from './SecurityPage';

vi.mock('../../lib/authApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/authApi')>()),
  fetchTotpStatus: vi.fn(),
  startTotpEnrollment: vi.fn(),
  confirmTotp: vi.fn(),
  disableOwnTotp: vi.fn(),
}));

import { confirmTotp, disableOwnTotp, fetchTotpStatus, startTotpEnrollment } from '../../lib/authApi';

const mockedStatus = vi.mocked(fetchTotpStatus);
const mockedStart = vi.mocked(startTotpEnrollment);
const mockedConfirm = vi.mocked(confirmTotp);
const mockedDisable = vi.mocked(disableOwnTotp);

const off = { enabled: false, available: true, requiredForAdmins: false, recoveryCodesRemaining: 0 };

function renderPage(isAdmin = false) {
  const user = { id: 'u1', username: 'alice', kind: 'human', displayName: 'Alice', isAdmin } as User;
  return render(
    <MemoryRouter initialEntries={['/settings/security']}>
      <AuthProvider>
        <ToastProvider>
          <Routes>
            <Route element={<Outlet context={{ user }} />}>
              <Route path="/settings/security" element={<SecurityPage />} />
            </Route>
          </Routes>
        </ToastProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('SecurityPage', () => {
  beforeEach(() => {
    mockedStatus.mockReset().mockResolvedValue(off);
    mockedStart.mockReset();
    mockedConfirm.mockReset();
    mockedDisable.mockReset();
  });

  it('attiva il TOTP: QR e chiave, conferma con un codice, codici di recupero una sola volta', async () => {
    mockedStart.mockResolvedValue({
      secret: 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ',
      otpauthUri: 'otpauth://totp/GitStack:alice?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=GitStack',
    });
    mockedConfirm.mockResolvedValue(['AAAAA-BBBBB', 'CCCCC-DDDDD']);
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole('button', { name: 'Set up two-factor authentication' }));

    const form = await screen.findByRole('form', { name: 'Set up two-factor authentication' });
    expect(within(form).getByRole('img', { name: /qr code/i })).toBeInTheDocument();
    expect(within(form).getByText('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ')).toBeInTheDocument();

    await user.type(within(form).getByLabelText('Authentication code'), '123456');
    await user.click(within(form).getByRole('button', { name: 'Verify and turn on' }));

    await waitFor(() => expect(mockedConfirm).toHaveBeenCalledWith('123456'));
    const region = await screen.findByRole('region', { name: 'Recovery codes' });
    expect(within(region).getByText('AAAAA-BBBBB')).toBeInTheDocument();
    expect(within(region).getByText(/won.t be able to see them again/i)).toBeInTheDocument();

    await user.click(within(region).getByRole('button', { name: 'I saved these codes' }));
    expect(await screen.findByText(/two-factor authentication is/i)).toBeInTheDocument();
    expect(screen.queryByText('AAAAA-BBBBB')).not.toBeInTheDocument();
  });

  it('un codice sbagliato lascia l\'attivazione in corso con un messaggio', async () => {
    mockedStart.mockResolvedValue({ secret: 'GEZDGNBVGY3TQOJQ', otpauthUri: 'otpauth://totp/GitStack:alice?secret=GEZDGNBVGY3TQOJQ' });
    mockedConfirm.mockRejectedValue(
      new ApiError({ error: { code: 'validation_failed', message: 'Dati non validi.', details: { fields: { code: 'codice non valido' } } } }, 422),
    );
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Set up two-factor authentication' }));
    await user.type(await screen.findByLabelText('Authentication code'), '000000');
    await user.click(screen.getByRole('button', { name: 'Verify and turn on' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/code is not valid/i);
    expect(screen.getByLabelText('Authentication code')).toBeInTheDocument();
  });

  it('con un codice di sei cifre non valido non chiama il server', async () => {
    mockedStart.mockResolvedValue({ secret: 'GEZDGNBVGY3TQOJQ', otpauthUri: 'otpauth://totp/GitStack:alice?secret=GEZDGNBVGY3TQOJQ' });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Set up two-factor authentication' }));
    await user.type(await screen.findByLabelText('Authentication code'), '12');
    await user.click(screen.getByRole('button', { name: 'Verify and turn on' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/6-digit/i);
    expect(mockedConfirm).not.toHaveBeenCalled();
  });

  it('disattiva con password e codice', async () => {
    mockedStatus.mockResolvedValue({ enabled: true, available: true, requiredForAdmins: false, recoveryCodesRemaining: 7 });
    mockedDisable.mockResolvedValue();
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText(/7 unused recovery codes/i)).toBeInTheDocument();
    const form = screen.getByRole('form', { name: 'Turn off two-factor authentication' });
    await user.type(within(form).getByLabelText('Password'), 'la mia password');
    await user.type(within(form).getByLabelText('Authentication code'), '654 321');
    await user.click(within(form).getByRole('button', { name: 'Turn off two-factor authentication' }));

    await waitFor(() => expect(mockedDisable).toHaveBeenCalledWith('la mia password', { totpCode: '654321' }));
    expect(await screen.findByRole('button', { name: 'Set up two-factor authentication' })).toBeInTheDocument();
  });

  it('un admin con TOTP obbligatorio lo vede attivo e non puo disattivarlo', async () => {
    mockedStatus.mockResolvedValue({ enabled: true, available: true, requiredForAdmins: true, recoveryCodesRemaining: 10 });
    renderPage(true);
    expect(await screen.findByText(/requires administrators to keep it on/i)).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'Turn off two-factor authentication' })).not.toBeInTheDocument();
  });

  it('un admin senza TOTP in un\'installazione che lo impone vede l\'avviso', async () => {
    mockedStatus.mockResolvedValue({ ...off, requiredForAdmins: true });
    renderPage(true);
    expect(await screen.findByRole('alert')).toHaveTextContent(/requires administrators to use two-factor/i);
  });

  it('senza chiavi sul server spiega che non e disponibile', async () => {
    mockedStatus.mockResolvedValue({ ...off, available: false });
    renderPage();
    expect(await screen.findByText(/not available/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Set up two-factor authentication' })).not.toBeInTheDocument();
  });
});
