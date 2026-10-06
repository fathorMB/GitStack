import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Topbar } from './Topbar';

vi.mock('../lib/authApi', () => ({ signOut: vi.fn() }));
vi.mock('../lib/notificationsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/notificationsApi')>()),
  fetchUnreadCount: vi.fn(),
}));

import { fetchUnreadCount, notifyNotificationsChanged } from '../lib/notificationsApi';

const mockedCount = vi.mocked(fetchUnreadCount);

function renderBar() {
  return render(
    <MemoryRouter>
      <Topbar crumb="Test" />
    </MemoryRouter>,
  );
}

describe('Topbar: contatore delle notifiche', () => {
  beforeEach(() => {
    mockedCount.mockReset();
  });

  it('mostra il numero di non lette e lo aggiorna al cambio della casella', async () => {
    mockedCount.mockResolvedValue(3);
    renderBar();
    expect(await screen.findByRole('link', { name: 'Notifications, 3 unread' })).toHaveAttribute('href', '/notifications');
    mockedCount.mockResolvedValue(0);
    notifyNotificationsChanged();
    expect(await screen.findByRole('link', { name: 'Notifications' })).toBeInTheDocument();
  });

  it('con il backend in errore resta senza contatore', async () => {
    mockedCount.mockImplementation(() => new Promise((_, reject) => setTimeout(() => reject(new Error('501')), 0)));
    renderBar();
    expect(await screen.findByRole('link', { name: 'Notifications' })).toBeInTheDocument();
  });
});
