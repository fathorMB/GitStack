import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ToastProvider } from '../../components';
import { ApiError } from '../../lib/http';
import { IssueSubscriptionBox } from '../repos/IssueSubscriptionBox';
import { RepoWatchSelect } from '../repos/RepoWatchSelect';
import { NotificationSettingsPage } from './NotificationSettingsPage';
import { SettingsLayout } from './SettingsLayout';

vi.mock('../../lib/notificationsApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/notificationsApi')>()),
  fetchNotificationPreferences: vi.fn(),
  saveEmailPreferences: vi.fn(),
  fetchRepoWatch: vi.fn(),
  saveRepoWatch: vi.fn(),
  fetchIssueSubscription: vi.fn(),
  subscribeToIssue: vi.fn(),
  unsubscribeFromIssue: vi.fn(),
}));
vi.mock('../../lib/reposApi', async (orig) => ({ ...(await orig<typeof import('../../lib/reposApi')>()), fetchRepos: vi.fn() }));
vi.mock('../../lib/authApi', async (orig) => ({ ...(await orig<typeof import('../../lib/authApi')>()), fetchSession: vi.fn() }));

import { fetchSession } from '../../lib/authApi';
import {
  fetchIssueSubscription,
  fetchNotificationPreferences,
  fetchRepoWatch,
  saveEmailPreferences,
  saveRepoWatch,
  subscribeToIssue,
  unsubscribeFromIssue,
} from '../../lib/notificationsApi';
import type { NotificationPreferences } from '../../lib/notificationsApi';
import { fetchRepos } from '../../lib/reposApi';

const allOff = { assigned: false, mentioned: true, participating: false, subscribed: false, commit_linked: false, state_change: false, webhook: false };
const prefs = (emailAvailable: boolean): NotificationPreferences => ({ emailAvailable, email: allOff });

function renderSettings() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/settings/notifications']}>
        <Routes>
          <Route path="/settings" element={<SettingsLayout />}>
            <Route path="notifications" element={<NotificationSettingsPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  );
}

beforeEach(() => {
  // Radix Select in jsdom: manca il pointer capture.
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => undefined;
  Element.prototype.releasePointerCapture = () => undefined;
  Element.prototype.scrollIntoView = () => undefined;
  vi.mocked(fetchSession).mockReset().mockResolvedValue({ user: { id: 'u', username: 'mrossi', displayName: 'Mario', email: 'm@acme.local' } } as never);
  vi.mocked(fetchNotificationPreferences).mockReset().mockResolvedValue(prefs(true));
  vi.mocked(saveEmailPreferences).mockReset();
  vi.mocked(fetchRepos).mockReset().mockResolvedValue([{ id: 'r1', name: 'api', owner: { name: 'acme' } }] as never);
  vi.mocked(fetchRepoWatch).mockReset().mockResolvedValue({ mode: 'all' });
  vi.mocked(saveRepoWatch).mockReset();
  vi.mocked(fetchIssueSubscription).mockReset();
  vi.mocked(subscribeToIssue).mockReset();
  vi.mocked(unsubscribeFromIssue).mockReset();
});

describe('NotificationSettingsPage', () => {
  it('con SMTP mostra le opzioni email e salva il cambio', async () => {
    vi.mocked(saveEmailPreferences).mockResolvedValue({ emailAvailable: true, email: { ...allOff, assigned: true } });
    renderSettings();
    const box = await screen.findByRole('checkbox', { name: 'Assignments by email' }, { timeout: 4000 });
    expect(screen.getByRole('checkbox', { name: 'Mentions by email' })).toBeChecked();
    expect(box).not.toBeChecked();
    await userEvent.click(box);
    await waitFor(() => expect(saveEmailPreferences).toHaveBeenCalledWith({ assigned: true }));
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'Assignments by email' })).toBeChecked());
    expect(screen.getByText(/removed after 90 days/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Notifications' })).toBeInTheDocument();
  }, 15000);

  it('senza SMTP nasconde le opzioni email e mostra l avviso', async () => {
    vi.mocked(fetchNotificationPreferences).mockResolvedValue(prefs(false));
    renderSettings();
    expect(await screen.findByText(/configured an email server/, undefined, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /by email/ })).not.toBeInTheDocument();
  }, 15000);

  it('se le preferenze rispondono 501 la pagina resta in piedi', async () => {
    vi.mocked(fetchNotificationPreferences).mockRejectedValue(new ApiError({ error: { code: 'not_implemented', message: 'Not implemented' } }, 501));
    renderSettings();
    expect(await screen.findByText('Not implemented', undefined, { timeout: 4000 })).toBeInTheDocument();
    expect(await screen.findByText('acme/api')).toBeInTheDocument();
  }, 15000);

  it('elenca i repo col loro Watch e lo cambia', async () => {
    vi.mocked(saveRepoWatch).mockResolvedValue({ mode: 'ignore' });
    renderSettings();
    const trigger = await screen.findByRole('combobox', { name: 'Watch acme/api' }, { timeout: 4000 });
    expect(trigger).toHaveTextContent('All activity');
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole('option', { name: 'Ignore' }));
    await waitFor(() => expect(saveRepoWatch).toHaveBeenCalledWith('acme', 'api', 'ignore'));
    await waitFor(() => expect(trigger).toHaveTextContent('Ignore'));
  }, 15000);
});

describe('RepoWatchSelect', () => {
  it('nella pagina del repo cambia il Watch e ripristina se il salvataggio fallisce', async () => {
    vi.mocked(fetchRepoWatch).mockResolvedValue({ mode: 'participating' });
    vi.mocked(saveRepoWatch).mockRejectedValue(new ApiError({ error: { code: 'forbidden', message: 'Nope' } }, 403));
    render(
      <ToastProvider>
        <RepoWatchSelect owner="acme" repo="api" />
      </ToastProvider>,
    );
    const trigger = await screen.findByRole('combobox', { name: 'Watch acme/api' });
    expect(screen.getByText('Watch:')).toBeInTheDocument();
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole('option', { name: 'All activity' }));
    await waitFor(() => expect(saveRepoWatch).toHaveBeenCalledWith('acme', 'api', 'all'));
    await waitFor(() => expect(trigger).toHaveTextContent('Participating'));
    expect(screen.getByRole('alert')).toHaveTextContent('Nope');
  }, 15000);
});

describe('IssueSubscriptionBox', () => {
  it('Unsubscribe e poi Subscribe, col motivo', async () => {
    vi.mocked(fetchIssueSubscription).mockResolvedValue({ subscribed: true, reason: 'author' });
    vi.mocked(unsubscribeFromIssue).mockResolvedValue({ subscribed: false, reason: 'none' });
    vi.mocked(subscribeToIssue).mockResolvedValue({ subscribed: true, reason: 'manual' });
    render(<IssueSubscriptionBox owner="acme" repo="api" number={12} />);
    expect(await screen.findByText("You're receiving notifications because you're the author.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Unsubscribe' }));
    await waitFor(() => expect(unsubscribeFromIssue).toHaveBeenCalledWith('acme', 'api', 12));
    expect(await screen.findByText("You're not receiving notifications for this issue.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    await waitFor(() => expect(subscribeToIssue).toHaveBeenCalledWith('acme', 'api', 12));
    expect(await screen.findByText(/because you subscribed/)).toBeInTheDocument();
  }, 15000);
});
