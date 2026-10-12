import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PullMirrorBadge, PullMirrorInfoBanner } from './PullMirrorBanner';
import type { PullMirror } from '@gitstack/api-client';

function pullMirror(over: Partial<PullMirror> = {}): PullMirror {
  return {
    id: 'pm1',
    origin: 'https://github.com/acme/example.git',
    mode: 'mirror',
    intervalSeconds: 600,
    state: 'in_sync',
    hasToken: false,
    attempts: 0,
    divergedRefs: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-12T00:00:00Z',
    lastSyncedAt: '2026-10-12T10:00:00Z',
    ...over,
  } as PullMirror;
}

describe('PullMirrorBadge', () => {
  it('renders badge when mirror is present', () => {
    render(<PullMirrorBadge mirror={{ origin: 'https://github.com/acme/example.git', lastSyncedAt: '2026-10-12T10:00:00Z' }} />);
    expect(screen.getByText('Mirror')).toBeInTheDocument();
  });

  it('renders nothing without mirror', () => {
    const { container } = render(<PullMirrorBadge mirror={null} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing without origin', () => {
    const { container } = render(<PullMirrorBadge mirror={{}} />);
    expect(container.firstChild).toBeNull();
  });
});

describe('PullMirrorInfoBanner', () => {
  it('renders nothing without mirror', () => {
    const { container } = render(<PullMirrorInfoBanner mirror={null} />);
    expect(container.firstChild).toBeNull();
  });

  it('info banner for active mirror includes push to source', () => {
    render(<PullMirrorInfoBanner mirror={pullMirror()} />);
    const alert = screen.getByRole('note');
    expect(alert).toHaveClass('alert-info');
    expect(alert).toHaveTextContent('This repository is a pull mirror: pushes are rejected here.');
    expect(alert).toHaveTextContent('Push to');
  });

  it('info banner for paused mirror', () => {
    render(<PullMirrorInfoBanner mirror={pullMirror({ state: 'paused' })} />);
    const alert = screen.getByRole('note');
    expect(alert).toHaveTextContent('paused');
  });

  it('warning banner for diverged refs', () => {
    render(
      <PullMirrorInfoBanner
        mirror={pullMirror({
          state: 'in_sync',
          divergedRefs: [{ ref: 'refs/heads/old-feature', kind: 'rewritten', localSha: 'abc123', detectedAt: '2026-10-12T10:00:00Z' }],
        })}
      />
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('alert-warning');
    expect(alert).toHaveTextContent('Some refs have diverged');
    expect(alert).toHaveTextContent('old-feature');
  });

  it('warning banner diverged shows ref list when present', () => {
    render(
      <PullMirrorInfoBanner
        mirror={pullMirror({
          state: 'in_sync',
          divergedRefs: [
            { ref: 'refs/heads/old', kind: 'rewritten', localSha: 'abc', detectedAt: '2026-10-12T10:00:00Z' },
            { ref: 'refs/heads/next', kind: 'deleted', localSha: 'def', detectedAt: '2026-10-12T10:00:00Z' },
          ],
        })}
      />
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('alert-warning');
    expect(alert).toHaveTextContent('Some refs have diverged');
    expect(alert).toHaveTextContent('old');
    expect(alert).toHaveTextContent('next');
  });

  it('danger banner for stopped mirror', () => {
    render(
      <PullMirrorInfoBanner
        mirror={pullMirror({
          state: 'stopped',
          lastError: 'authentication failed (401)',
          lastSyncedAt: '2026-10-11T10:00:00Z',
        })}
      />
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('alert-danger');
    expect(alert).toHaveTextContent(/No successful update for \d+ hour/);
    expect(alert).toHaveTextContent('authentication failed (401)');
  });

  it('danger banner without lastSyncedAt', () => {
    render(
      <PullMirrorInfoBanner
        mirror={pullMirror({
          state: 'stopped',
          lastSyncedAt: null,
        })}
      />
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('The mirror has never synced');
  });

  it('no push to source for once mode', () => {
    render(<PullMirrorInfoBanner mirror={pullMirror({ mode: 'once' })} />);
    const alert = screen.getByRole('note');
    expect(alert).not.toHaveTextContent('Push to');
  });

  it('no push to source for stopped state', () => {
    render(<PullMirrorInfoBanner mirror={pullMirror({ state: 'stopped' })} />);
    const alert = screen.getByRole('alert');
    expect(alert).not.toHaveTextContent('Push to');
  });
});
