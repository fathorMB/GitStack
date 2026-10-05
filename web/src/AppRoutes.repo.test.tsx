import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppRoutes } from './App';
import { ToastProvider } from './components';
import { AuthProvider } from './routes/auth';

vi.mock('./lib/authApi', () => ({
  fetchSession: vi.fn().mockResolvedValue({ user: { username: 'nobody', isAdmin: false } }),
  fetchOidcProviders: vi.fn().mockResolvedValue([]),
  signOut: vi.fn(),
}));
vi.mock('./pages/repos/RepoPage', () => ({
  RepoPage: ({ mode }: { mode?: string }) => <div data-testid="repo-page">{mode ?? 'tree'}</div>,
}));

function at(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <ToastProvider>
          <AppRoutes />
        </ToastProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('rotte del browser del codice', () => {
  it.each([
    ['/acme/api/tree/main/cmd', 'tree'],
    ['/acme/api/blob/main/cmd/main.go', 'blob'],
    ['/acme/api/blame/feature/x/cmd/main.go', 'blame'],
    ['/acme/api/commits', 'commits'],
    ['/acme/api/commits/main/cmd/main.go', 'commits'],
    ['/acme/api/commit/4e1a9c0', 'commit'],
    ['/acme/api/tags', 'tags'],
    ['/acme/api/search', 'search'],
  ])('%s apre la pagina del repo in modo %s', async (path, mode) => {
    at(path);
    expect(await screen.findByTestId('repo-page')).toHaveTextContent(mode);
  });
});
