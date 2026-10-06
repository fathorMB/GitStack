import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from './Sidebar';

vi.mock('../lib/notificationsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/notificationsApi')>()),
  fetchUnreadCount: vi.fn(),
}));

import { fetchUnreadCount } from '../lib/notificationsApi';

const mockedCount = vi.mocked(fetchUnreadCount);

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Sidebar />
    </MemoryRouter>,
  );
}

describe('Sidebar: voce Notifications', () => {
  beforeEach(() => {
    mockedCount.mockReset();
  });

  it('mostra il contatore delle non lette e sulla rotta /notifications e\' attiva', async () => {
    mockedCount.mockResolvedValue(3);
    renderAt('/notifications');
    const link = await screen.findByRole('link', { name: /Notifications/ });
    expect(link).toHaveAttribute('href', '/notifications');
    expect(link).toHaveClass('active');
    expect(await screen.findByText('3')).toHaveClass('counter');
  });

  it('fuori da /notifications non e\' attiva; a zero o con errore niente counter', async () => {
    mockedCount.mockImplementation(async () => {
      throw new Error('501');
    });
    renderAt('/repos');
    const link = await screen.findByRole('link', { name: /Notifications/ });
    expect(link).not.toHaveClass('active');
    expect(link.querySelector('.counter')).toBeNull();
  });
});
