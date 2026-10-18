import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CombinedCommitStatus, PullRequest, PullRequestMergeability, PullRequestReview } from '../../lib/pullsApi';
import { PullMergeBox } from './PullMergeBox';
import type { PullMergeBoxProps } from './PullMergeBox';

const human = { id: 'u1', username: 'lbianchi', kind: 'human' as const };
const agent = { id: 'u2', username: 'review-bot', kind: 'agent' as const };

const pr = (over: Partial<PullRequest> = {}): PullRequest => ({
  id: 'p1',
  number: 58,
  title: 'fix',
  body: '',
  state: 'open',
  draft: false,
  author: { id: 'u3', username: 'build-agent', kind: 'agent' },
  sourceBranch: 'agent/57-webhook-410',
  targetBranch: 'main',
  headSha: 'abc1234',
  sourceBranchExists: true,
  requestedReviewers: [],
  reviewDecision: 'approved',
  approvals: 1,
  requiredApprovals: 1,
  commentCount: 0,
  unresolvedThreadCount: 0,
  closingIssues: [57],
  commitCount: 3,
  changedFiles: 2,
  additions: 1,
  deletions: 1,
  createdAt: '2026-10-18T10:00:00Z',
  updatedAt: '2026-10-18T10:00:00Z',
  ...over,
});

const mergeability = (over: Partial<PullRequestMergeability> = {}): PullRequestMergeability => ({
  state: 'mergeable',
  mergeable: true,
  conflictFiles: [],
  behindBy: 0,
  aheadBy: 3,
  blockers: [],
  allowedMethods: ['squash', 'merge'],
  defaultMethod: 'squash',
  approvals: 1,
  requiredApprovals: 1,
  headSha: 'abc1234',
  ...over,
});

const okStatus: CombinedCommitStatus = {
  sha: 'abc1234',
  state: 'success',
  totalCount: 2,
  requiredContexts: [{ context: 'test', state: 'success', satisfied: true }],
  statuses: [
    { id: 's1', sha: 'abc1234', state: 'success', context: 'test', description: 'Jenkins · 212 tests passed', targetUrl: 'https://ci.example/1', creator: human, createdAt: '2026-10-18T10:00:00Z' },
    { id: 's2', sha: 'abc1234', state: 'success', context: 'lint', description: 'Woodpecker · no issues', creator: human, createdAt: '2026-10-18T10:00:00Z' },
  ],
};

const review = (over: Partial<PullRequestReview>): PullRequestReview => ({
  id: 'r1',
  author: human,
  state: 'approved',
  body: '',
  commitSha: 'abc1234',
  stale: false,
  countsAsApproval: true,
  commentCount: 0,
  submittedAt: '2026-10-18T10:00:00Z',
  ...over,
});

function setup(over: Partial<PullMergeBoxProps> = {}) {
  const props: PullMergeBoxProps = {
    pr: pr(),
    mergeability: mergeability(),
    status: okStatus,
    reviews: [review({})],
    canWrite: true,
    isAuthor: false,
    onMerge: vi.fn(),
    onReady: vi.fn(),
    onDeleteBranch: vi.fn(),
    onRerequest: vi.fn(),
    ...over,
  };
  const view = render(<PullMergeBox {...props} />);
  return { props, ...view };
}

describe('PullMergeBox: varianti', () => {
  it('pronta: tre righe verdi, squash di default e Delete branch after merge', () => {
    const { props, container } = setup();
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'ready');
    expect(container.querySelector('.mergebox.ok')).toHaveAttribute('data-variant', 'ready');
    expect(screen.getByText('Approved')).toBeInTheDocument();
    expect(screen.getByText('All required checks passed')).toBeInTheDocument();
    expect(screen.getByText('No conflicts with the base branch')).toBeInTheDocument();
    expect(screen.getByText('Required')).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('Delete branch after merge'));
    fireEvent.click(screen.getByRole('button', { name: 'Squash and merge' }));
    expect(props.onMerge).toHaveBeenCalledWith('squash', true);
  });

  it('il metodo si sceglie dal menu e dal link «or»', () => {
    const { props } = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Merge method' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Create a merge commit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Create a merge commit' }));
    expect(props.onMerge).toHaveBeenCalledWith('merge', false);
  });

  it('con un solo metodo ammesso non c’è scelta', () => {
    setup({ mergeability: mergeability({ allowedMethods: ['merge'], defaultMethod: 'merge' }) });
    expect(screen.getByRole('button', { name: 'Create a merge commit' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Merge method' })).toBeDisabled();
  });

  it('serve approvazione: l’approvazione dell’agente non conta, quella superata è annullata, Merge disattivato', () => {
    const { props, container } = setup({
      pr: pr({ reviewDecision: 'required', approvals: 0 }),
      mergeability: mergeability({ state: 'blocked', mergeable: false, approvals: 0, blockers: [{ code: 'approvals_required', message: 'x' }] }),
      reviews: [review({ id: 'a', author: agent, countsAsApproval: false }), review({ id: 'b', stale: true, countsAsApproval: false })],
    });
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'approval');
    expect(container.querySelector('.mergebox.warn')).toHaveAttribute('data-variant', 'approval');
    expect(screen.getByText('Review required')).toBeInTheDocument();
    expect(screen.getByText(/agent approvals don.t count/)).toBeInTheDocument();
    expect(screen.getByText(/lbianchi.s approval was dismissed/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Squash and merge' })).toBeDisabled();
    expect(screen.getByText(/They apply to everyone, admins included/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Re-request review' }));
    expect(props.onRerequest).toHaveBeenCalledWith(['lbianchi']);
  });

  it('stato rosso: il contesto obbligatorio fallito blocca il merge', () => {
    const status: CombinedCommitStatus = {
      ...okStatus,
      state: 'failure',
      requiredContexts: [{ context: 'test', state: 'failure', satisfied: false }],
      statuses: [{ ...okStatus.statuses[0], state: 'failure', description: '2 of 212 tests failed' }, okStatus.statuses[1]],
    };
    const { container } = setup({
      status,
      mergeability: mergeability({ state: 'blocked', mergeable: false, blockers: [{ code: 'status_failed', message: 'x', contexts: ['test'] }] }),
    });
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'failing');
    expect(container.querySelector('.mergebox.bad')).toHaveAttribute('data-variant', 'failing');
    expect(screen.getByText('Some required checks failed')).toBeInTheDocument();
    expect(screen.getByText('2 of 212 tests failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Squash and merge' })).toBeDisabled();
    expect(screen.getByText(/must succeed on the latest commit/)).toBeInTheDocument();
  });

  it('conflitti: elenca i file e le istruzioni git e gs, nessuna modifica nel browser', () => {
    const { container } = setup({
      mergeability: mergeability({ state: 'conflicts', mergeable: false, conflictFiles: ['internal/webhooks/deliver.go', 'internal/webhooks/deliver_test.go'], blockers: [{ code: 'conflicts', message: 'x' }] }),
    });
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'conflicts');
    expect(container.querySelector('.mergebox.bad')).toHaveAttribute('data-variant', 'conflicts');
    expect(screen.getByText('This branch has conflicts that must be resolved')).toBeInTheDocument();
    const files = within(screen.getByRole('list', { name: 'Conflicting files' })).getAllByRole('listitem');
    expect(files.map((f) => f.textContent)).toEqual(['internal/webhooks/deliver.go', 'internal/webhooks/deliver_test.go']);
    expect(screen.getByText(/gs pr checkout 58/)).toBeInTheDocument();
    expect(screen.getByText(/git merge origin\/main/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Squash and merge' })).toBeDisabled();
    expect(screen.getByText('The browser does not edit conflicts.')).toBeInTheDocument();
  });

  it('bozza: non si unisce, Ready for review', () => {
    const { props, container } = setup({ pr: pr({ draft: true }), mergeability: null });
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'draft');
    expect(screen.getByText('This pull request is still a draft')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /merge/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Ready for review' }));
    expect(props.onReady).toHaveBeenCalled();
  });

  it('unita: Delete branch finché il branch esiste', () => {
    const { props, container } = setup({ pr: pr({ state: 'merged' }), mergeability: null });
    expect(container.querySelector('.mergebox')).toHaveAttribute('data-variant', 'merged');
    expect(container.querySelector('.mergebox.merged')).toHaveAttribute('data-variant', 'merged');
    expect(screen.getByText('Pull request successfully merged and closed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Delete branch' }));
    expect(props.onDeleteBranch).toHaveBeenCalled();
  });

  it('unita con branch già eliminato: niente Delete branch', () => {
    setup({ pr: pr({ state: 'merged', sourceBranchExists: false }), mergeability: null });
    expect(screen.queryByRole('button', { name: 'Delete branch' })).not.toBeInTheDocument();
    expect(screen.getByText(/branch has been deleted/)).toBeInTheDocument();
  });

  it('chiusa: nessun pulsante di merge', () => {
    setup({ pr: pr({ state: 'closed' }), mergeability: null });
    expect(screen.getByText('This pull request is closed')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});

describe('PullMergeBox: permessi', () => {
  it('senza write il merge e Delete branch after merge non ci sono', () => {
    setup({ canWrite: false });
    expect(screen.queryByRole('button', { name: /merge/i })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Delete branch after merge')).not.toBeInTheDocument();
    expect(screen.getByText(/needs Write access/)).toBeInTheDocument();
  });

  it('senza write non si elimina il branch di una PR unita', () => {
    setup({ pr: pr({ state: 'merged' }), canWrite: false, mergeability: null });
    expect(screen.queryByRole('button', { name: 'Delete branch' })).not.toBeInTheDocument();
  });

  it('senza write e senza essere l’autore la bozza non si segna pronta; l’autore sì', () => {
    const { unmount } = setup({ pr: pr({ draft: true }), canWrite: false, isAuthor: false, mergeability: null });
    expect(screen.queryByRole('button', { name: 'Ready for review' })).not.toBeInTheDocument();
    unmount();
    setup({ pr: pr({ draft: true }), canWrite: false, isAuthor: true, mergeability: null });
    expect(screen.getByRole('button', { name: 'Ready for review' })).toBeInTheDocument();
  });

  it('senza write non c’è Re-request review', () => {
    setup({
      canWrite: false,
      pr: pr({ reviewDecision: 'required', approvals: 0 }),
      mergeability: mergeability({ mergeable: false, blockers: [{ code: 'approvals_required', message: 'x' }] }),
      reviews: [review({ stale: true, countsAsApproval: false })],
    });
    expect(screen.queryByRole('button', { name: 'Re-request review' })).not.toBeInTheDocument();
  });
});
