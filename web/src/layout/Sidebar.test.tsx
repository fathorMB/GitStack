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

  it('Apps e\' un link attivo su /apps; Databases e Home restano disattivate con Soon', async () => {
    mockedCount.mockResolvedValue(0);
    renderAt('/apps');
    const link = await screen.findByRole('link', { name: 'Apps' });
    expect(link).toHaveAttribute('href', '/apps');
    expect(link).toHaveClass('active');
    for (const name of [/Databases/, /Home/]) {
      const btn = screen.getByRole('button', { name });
      expect(btn).toBeDisabled();
      expect(btn).toHaveTextContent('Soon');
    }
  });

  it('raggiunge Database settings dal gruppo Administration (GIT-557)', async () => {
    mockedCount.mockResolvedValue(0);
    renderAt('/admin/databases');
    const link = await screen.findByRole('link', { name: 'Database settings' });
    expect(link).toHaveAttribute('href', '/admin/databases');
    expect(link).toHaveClass('active');
  });

  it('raggiunge Apps & resources dal gruppo Administration', async () => {
    mockedCount.mockResolvedValue(0);
    renderAt('/admin/apps');
    const link = await screen.findByRole('link', { name: 'Apps & resources' });
    expect(link).toHaveAttribute('href', '/admin/apps');
    expect(link).toHaveClass('active');
  });
});
