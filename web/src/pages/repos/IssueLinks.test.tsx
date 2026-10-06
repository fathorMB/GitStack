import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { groupCommitEvents, linkedCommits } from '../../lib/issueLinks';
import type { IssueEvent } from '../../lib/issuesApi';
import { ClosedByCommitEvent, LinkedCommitsBox, LinkedCommitsCount, LinkedCommitsEvent, ReferencedFromEvent } from './IssueLinks';

const bot = { id: 'b', username: 'build-agent', kind: 'agent' as const };
const commit = (sha: string, subject: string, ref = 'refs/heads/fix') => ({ sha, repository: 'acme/api', subject, ref, authorName: 'Build Agent' });
const ev = (id: string, type: IssueEvent['type'], data: Record<string, unknown>, at = '2026-10-04T12:00:00Z', actor: IssueEvent['actor'] = bot): IssueEvent => ({ id, type, actor, data, createdAt: at });

const inRouter = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

const e1 = ev('1', 'commit_linked', { commit: commit('c81f3e2aaaa', 'test(ssh): cover ed25519') });
const e2 = ev('2', 'commit_linked', { commit: commit('4e1a9c0bbbb', 'fix(ssh): accept ed25519 host keys (fixes #41)') }, '2026-10-04T12:00:05Z');

describe('referenced_from (C1)', () => {
  it('mostra «referenced this issue from owner/repo#n» con link alla issue', () => {
    inRouter(<ReferencedFromEvent e={ev('r', 'referenced_from', { source: { kind: 'issue', repository: 'acme/web', number: 7, title: 'Docs' } }, '2026-10-04T12:00:00Z', { id: 'm', username: 'mrossi', kind: 'human' })} />);
    const row = screen.getByTestId('event-referenced_from');
    expect(row).toHaveTextContent('mrossi referenced this issue from acme/web#7');
    expect(within(row).getByRole('link', { name: 'acme/web#7' })).toHaveAttribute('href', '/acme/web/issues/7');
  });

  it('senza source valida non rende nulla', () => {
    inRouter(<ReferencedFromEvent e={ev('r', 'referenced_from', {})} />);
    expect(screen.queryByTestId('event-referenced_from')).not.toBeInTheDocument();
  });
});

describe('commit collegati (C2)', () => {
  it('raggruppa i commit dello stesso push e li elenca con link al dettaglio', () => {
    const groups = groupCommitEvents([e1, e2]);
    expect(groups).toHaveLength(1);
    inRouter(<LinkedCommitsEvent group={groups[0]} repoFullName="acme/api" />);
    expect(screen.getByTestId('event-commit_linked')).toHaveTextContent('build-agent pushed 2 commits referencing this issue');
    const list = screen.getByTestId('linked-commit-list');
    expect(within(list).getByRole('link', { name: 'c81f3e2' })).toHaveAttribute('href', '/acme/api/commit/c81f3e2aaaa');
    expect(within(list).getByText('fix(ssh): accept ed25519 host keys (fixes #41)')).toBeInTheDocument();
  });

  it('un solo commit: singolare; push distanti restano gruppi separati', () => {
    const far = ev('3', 'commit_linked', { commit: commit('aaaaaaa1', 'later') }, '2026-10-04T14:00:00Z');
    const groups = groupCommitEvents([e1, far]);
    expect(groups).toHaveLength(2);
    inRouter(<LinkedCommitsEvent group={groups[0]} repoFullName="acme/api" />);
    expect(screen.getByTestId('event-commit_linked')).toHaveTextContent('pushed 1 commit referencing');
  });

  it('nessun evento: nessun gruppo', () => {
    expect(groupCommitEvents([])).toEqual([]);
  });

  it('closed_by_commit: «Closed as completed by commit <sha> on <branch>»', () => {
    inRouter(<ClosedByCommitEvent e={ev('c', 'closed_by_commit', { commit: commit('4e1a9c0bbbb', 's', 'refs/heads/main'), reason: 'completed' }, '2026-10-04T12:00:00Z', null)} repoFullName="acme/api" defaultBranch="main" />);
    const row = screen.getByTestId('event-closed_by_commit');
    expect(row).toHaveTextContent('Closed as completed by commit 4e1a9c0 on main');
    expect(within(row).getByRole('link', { name: '4e1a9c0' })).toHaveAttribute('href', '/acme/api/commit/4e1a9c0bbbb');
  });

  it('closed_by_commit senza ref usa il branch principale', () => {
    inRouter(<ClosedByCommitEvent e={ev('c', 'closed_by_commit', { commit: commit('4e1a9c0bbbb', 's', '') }, '2026-10-04T12:00:00Z', null)} repoFullName="acme/api" defaultBranch="trunk" />);
    expect(screen.getByTestId('event-closed_by_commit')).toHaveTextContent('on trunk');
  });
});

describe('riquadro Linked commits', () => {
  it('elenca i commit senza duplicati (commit_linked + closed_by_commit)', () => {
    const closed = ev('c', 'closed_by_commit', { commit: commit('4e1a9c0bbbb', 'fix(ssh)', 'refs/heads/main') }, '2026-10-04T12:01:00Z', null);
    const commits = linkedCommits([e1, e2, closed]);
    expect(commits.map((c) => c.sha)).toEqual(['c81f3e2aaaa', '4e1a9c0bbbb']);
    inRouter(<LinkedCommitsBox commits={commits} repoFullName="acme/api" />);
    const box = screen.getByTestId('linked-commits-box');
    expect(within(box).getByRole('heading', { name: 'Linked commits' })).toBeInTheDocument();
    expect(within(box).getAllByRole('link')).toHaveLength(2);
  });

  it('senza collegamenti mostra «No linked commits»', () => {
    inRouter(<LinkedCommitsBox commits={[]} repoFullName="acme/api" />);
    expect(screen.getByText('No linked commits')).toBeInTheDocument();
  });
});

describe('conteggio nella lista', () => {
  it('mostra «N linked commits» e niente a zero', () => {
    const { rerender } = render(<LinkedCommitsCount count={2} />);
    expect(screen.getByTestId('linked-commit-count')).toHaveTextContent('2 linked commits');
    rerender(<LinkedCommitsCount count={0} />);
    expect(screen.queryByTestId('linked-commit-count')).not.toBeInTheDocument();
  });
});
