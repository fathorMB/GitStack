import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { SessionsPage } from './SessionsPage';

vi.mock('../../lib/accessApi', () => ({
  fetchSessions: vi.fn(),
  endSession: vi.fn(),
}));

import { endSession, fetchSessions } from '../../lib/accessApi';

const mockedList = vi.mocked(fetchSessions);
const mockedEnd = vi.mocked(endSession);

const current = {
  id: 's-1',
  authMethod: 'password' as const,
  userAgent: 'Firefox on Linux',
  ipAddress: '192.0.2.1',
  createdAt: '2026-10-12T08:00:00Z',
  lastSeenAt: '2026-10-13T08:00:00Z',
  expiresAt: '2026-10-19T08:00:00Z',
  current: true,
};
const other = { ...current, id: 's-2', userAgent: 'Chrome on Windows', ipAddress: '192.0.2.2', current: false };

function renderPage() {
  return render(
    <ToastProvider>
      <SessionsPage />
    </ToastProvider>,
  );
}

describe('SessionsPage', () => {
  beforeEach(() => {
    mockedList.mockReset().mockResolvedValue([current, other]);
    mockedEnd.mockReset().mockResolvedValue(undefined);
  });

  it('elenca le sessioni e segna quella corrente', async () => {
    renderPage();
    expect(await screen.findByText('Firefox on Linux')).toBeInTheDocument();
    expect(screen.getByText('Chrome on Windows')).toBeInTheDocument();
    expect(screen.getAllByText('This session')).toHaveLength(1);
  });

  it('termina una sessione dopo la conferma e la toglie dall\'elenco', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('Chrome on Windows');
    await user.click(screen.getByRole('button', { name: 'End session Chrome on Windows' }));
    await user.click(await screen.findByRole('button', { name: 'End session' }));
    await waitFor(() => expect(mockedEnd).toHaveBeenCalledWith('s-2'));
    await waitFor(() => expect(screen.queryByText('Chrome on Windows')).not.toBeInTheDocument());
    expect(screen.getByText('Firefox on Linux')).toBeInTheDocument();
  });

  it('stato vuoto', async () => {
    mockedList.mockResolvedValue([]);
    renderPage();
    expect(await screen.findByText('No active sessions')).toBeInTheDocument();
  });
});
