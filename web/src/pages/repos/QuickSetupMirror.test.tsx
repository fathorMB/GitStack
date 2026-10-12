import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { QuickSetup } from './RepoPage';
import type { Repository } from '../../lib/reposApi';

vi.mock('../../lib/reposApi', async (orig) => ({
  ...(await orig<typeof import('../../lib/reposApi')>()),
}));

function repo(over: Partial<Repository> = {}): Repository {
  return {
    id: 'r1',
    owner: { type: 'organization', name: 'acme' },
    name: 'api-gateway',
    fullName: 'acme/api-gateway',
    description: 'Single entry point',
    visibility: 'private',
    defaultBranch: 'main',
    protectDefaultBranch: true,
    archived: false,
    archivedAt: null,
    empty: true,
    cloneUrls: { https: 'https://git.acme.local/acme/api-gateway.git', ssh: 'ssh://git@git.acme.local:2222/acme/api-gateway.git' },
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-02T00:00:00Z',
    ...over,
  };
}

describe('QuickSetup', () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it('shows push instructions for normal empty repo', () => {
    render(<QuickSetup repo={repo()} />);
    expect(screen.getByRole('heading', { name: 'Push an existing repository' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Start from scratch' })).toBeInTheDocument();
  });

  it('hides push instructions for mirror empty repo', () => {
    render(<QuickSetup repo={repo({ mirror: { origin: 'https://github.com/acme/example.git', lastSyncedAt: '2026-10-12T10:00:00Z' } })} />);
    expect(screen.queryByRole('heading', { name: 'Push an existing repository' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Start from scratch' })).not.toBeInTheDocument();
    expect(screen.getByText(/clone only, push to the source/)).toBeInTheDocument();
  });

  it('shows CLI clone for both normal and mirror repos', () => {
    render(<QuickSetup repo={repo({ mirror: { origin: 'https://github.com/acme/example.git' } })} />);
    expect(screen.getByRole('heading', { name: 'With the GitStack CLI' })).toBeInTheDocument();
    expect(screen.getByText(/gs repo clone acme\/api-gateway/)).toBeInTheDocument();
  });

  it('shows clone URL and copy button', () => {
    render(<QuickSetup repo={repo({ mirror: { origin: 'https://github.com/acme/example.git' } })} />);
    const input = screen.getByLabelText('Clone URL');
    expect(input).toHaveValue('https://git.acme.local/acme/api-gateway.git');
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument();
  });
});
