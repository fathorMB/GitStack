import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../lib/http';
import { NotificationsPage } from './NotificationsPage';

// L'API e' simulata: il backend risponde 501 finche' non c'e' GIT-133.
vi.mock('../lib/notificationsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/notificationsApi')>()),
  fetchNotifications: vi.fn(),
  fetchUnreadCount: vi.fn(),
  setNotificationRead: vi.fn(),
  setNotificationArchived: vi.fn(),
  removeNotification: vi.fn(),
  markAllRead: vi.fn(),
}));

import {
  fetchNotifications,
  fetchUnreadCount,
  markAllRead,
  removeNotification,
  setNotificationArchived,
  setNotificationRead,
} from '../lib/notificationsApi';
import type { Notification } from '../lib/notificationsApi';

const mockedList = vi.mocked(fetchNotifications);
const mockedCount = vi.mocked(fetchUnreadCount);
const mockedRead = vi.mocked(setNotificationRead);
const mockedArchive = vi.mocked(setNotificationArchived);
const mockedDelete = vi.mocked(removeNotification);
const mockedAll = vi.mocked(markAllRead);

const NOW = new Date().toISOString();

const mention: Notification = {
  id: 'n-1',
  reason: 'mentioned',
  read: false,
  archived: false,
  event: 'issue.comment_created',
  summary: 'mentioned you in',
  repository: { id: 'r-1', fullName: 'acme/identity' },
  issue: { number: 42, title: 'Support OIDC login', state: 'open' },
  actor: { id: 'u-1', username: 'gverdi', kind: 'human' },
  createdAt: NOW,
  readAt: null,
};
const agent: Notification = {
  id: 'n-2',
  reason: 'participating',
  read: false,
  archived: false,
  event: 'issue.comment_created',
  summary: 'finished work on',
  repository: { id: 'r-2', fullName: 'acme/api-gateway' },
  issue: { number: 41, title: 'Push over SSH fails', state: 'open' },
  actor: { id: 'u-2', username: 'build-agent', kind: 'agent' },
  createdAt: NOW,
  readAt: null,
};
const closed: Notification = {
  id: 'n-3',
  reason: 'state_change',
  read: true,
  archived: false,
  event: 'issue.closed',
  summary: 'Issue closed by commit 4e1a9c0:',
  repository: { id: 'r-2', fullName: 'acme/api-gateway' },
  issue: { number: 39, title: 'Rate limit headers missing', state: 'closed' },
  actor: null,
  createdAt: NOW,
  readAt: NOW,
};

function page(items: Notification[], unreadCount = items.filter((i) => !i.read).length) {
  return { items, page: 1, perPage: 30, total: items.length, unreadCount };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/notifications']}>
      <Routes>
        <Route path="/notifications" element={<NotificationsPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('NotificationsPage', () => {
  beforeEach(() => {
    mockedList.mockReset().mockResolvedValue(page([mention, agent]));
    mockedCount.mockReset().mockResolvedValue(2);
    mockedRead.mockReset().mockResolvedValue({ ...mention, read: true });
    mockedArchive.mockReset().mockResolvedValue({ ...mention, archived: true });
    mockedDelete.mockReset().mockResolvedValue(undefined);
    mockedAll.mockReset().mockResolvedValue(2);
  });

  it('mostra repo, issue, motivo e attore, con il badge agent', async () => {
    renderPage();
    const rows = await screen.findAllByTestId('notification');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText('gverdi')).toBeInTheDocument();
    expect(within(rows[0]).getByText('acme/identity')).toBeInTheDocument();
    expect(within(rows[0]).getByText('Mention')).toBeInTheDocument();
    expect(within(rows[0]).getByRole('link', { name: 'Support OIDC login' })).toHaveAttribute('href', '/acme/identity/issues/42');
    expect(within(rows[1]).getByText('agent')).toBeInTheDocument();
    expect(within(rows[0]).queryByText('agent')).not.toBeInTheDocument();
    expect(mockedList).toHaveBeenCalledWith('unread', 'all');
    expect(screen.getByRole('button', { name: 'Unread 2' })).toBeInTheDocument();
  });

  it('filtra per motivo', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByTestId('notification');
    mockedList.mockResolvedValue(page([mention]));
    await user.click(screen.getByRole('button', { name: 'Mentioned' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'mentioned'));
    expect(await screen.findAllByTestId('notification')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Webhooks' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'webhook'));
    await user.click(screen.getByRole('button', { name: 'Assigned' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'assigned'));
    await user.click(screen.getByRole('button', { name: 'Participating' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'participating'));
    await user.click(within(screen.getByRole('group', { name: 'Reason' })).getByRole('button', { name: 'All' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'all'));
  });

  it('passa da Unread ad All', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByTestId('notification');
    mockedList.mockResolvedValue(page([mention, agent, closed], 2));
    // "All" compare due volte (stato e motivo): il primo e' quello della casella.
    await user.click(within(screen.getByRole('group', { name: 'Inbox' })).getByRole('button', { name: 'All' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('all', 'all'));
    const all = await screen.findAllByTestId('notification');
    expect(all).toHaveLength(3);
    expect(all[0]).toHaveClass('unread');
    expect(all[2]).not.toHaveClass('unread');
    expect(screen.getByText('Issue closed by commit 4e1a9c0:')).toBeInTheDocument();
  });

  it('segna come letta e ricarica la casella', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByTestId('notification');
    mockedList.mockResolvedValue(page([agent]));
    await user.click(screen.getByRole('button', { name: 'Mark as read: mentioned you in' }));
    await waitFor(() => expect(mockedRead).toHaveBeenCalledWith('n-1', true));
    await waitFor(() => expect(screen.getAllByTestId('notification')).toHaveLength(1));
  });

  it('archivia ed elimina una voce', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByTestId('notification');
    await user.click(screen.getByRole('button', { name: 'Archive: mentioned you in' }));
    await waitFor(() => expect(mockedArchive).toHaveBeenCalledWith('n-1', true));
    await user.click(screen.getByRole('button', { name: 'Delete: finished work on' }));
    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith('n-2'));
  });

  it('segna tutte come lette, con il filtro per motivo corrente', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByTestId('notification');
    await user.click(screen.getByRole('button', { name: 'Mentioned' }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith('unread', 'mentioned'));
    mockedList.mockResolvedValue(page([]));
    await user.click(screen.getByRole('button', { name: /Mark all as read/ }));
    await waitFor(() => expect(mockedAll).toHaveBeenCalledWith('mentioned'));
    expect(await screen.findByText("You're all caught up")).toBeInTheDocument();
  });

  it('casella vuota: stato vuoto e Mark all disabilitato', async () => {
    mockedList.mockResolvedValue(page([]));
    renderPage();
    expect(await screen.findByText("You're all caught up")).toBeInTheDocument();
    expect(screen.queryAllByTestId('notification')).toHaveLength(0);
    expect(screen.getByRole('button', { name: /Mark all as read/ })).toBeDisabled();
  });

  it('un 501 del backend diventa un errore leggibile', async () => {
    mockedList.mockRejectedValue(new ApiError({ error: { code: 'not_implemented', message: 'not implemented' } }, 501));
    renderPage();
    expect(await screen.findByRole('alert')).toBeInTheDocument();
  });

  it('il link Settings porta alle preferenze', async () => {
    renderPage();
    await screen.findAllByTestId('notification');
    expect(screen.getByRole('link', { name: /Settings/ })).toHaveAttribute('href', '/settings/notifications');
  });
});
