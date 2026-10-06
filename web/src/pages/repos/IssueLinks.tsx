import { CircleCheck, GitCommitHorizontal, Link2 } from 'lucide-react';
import { Link } from 'react-router-dom';
import { timeAgo } from '../../lib/format';
import { branchName, commitOf, shortSha, sourceOf } from '../../lib/issueLinks';
import type { CommitGroup, LinkedCommit } from '../../lib/issueLinks';
import type { IssueEvent } from '../../lib/issuesApi';

const strong = { color: 'var(--text)' } as const;

function commitHref(c: LinkedCommit, fallbackRepo: string): string {
  return `/${c.repository || fallbackRepo}/commit/${c.sha}`;
}

/** C1: «<chi> referenced this issue from owner/repo#n». */
export function ReferencedFromEvent({ e }: { e: IssueEvent }) {
  const s = sourceOf(e);
  if (!s) return null;
  const label = `${s.repository}#${s.number}`;
  return (
    <div className="ev" data-testid="event-referenced_from">
      <span className="evi">
        <Link2 size={14} aria-hidden="true" />
      </span>
      <span>
        <b style={strong}>{e.actor?.username ?? 'GitStack'}</b> referenced this issue from{' '}
        {s.kind === 'issue' ? (
          <Link className="mono" to={`/${s.repository}/issues/${s.number}`} title={s.title}>
            {label}
          </Link>
        ) : (
          <span className="mono">{label}</span>
        )}
        {s.title ? <span className="muted"> {s.title}</span> : null} · {timeAgo(e.createdAt)}
      </span>
    </div>
  );
}

/** C2: «<chi> pushed N commits referencing this issue» + elenco con link al dettaglio del commit. */
export function LinkedCommitsEvent({ group, repoFullName }: { group: CommitGroup; repoFullName: string }) {
  const n = group.commits.length;
  return (
    <>
      <div className="ev" data-testid="event-commit_linked">
        <span className="evi commit">
          <GitCommitHorizontal size={14} aria-hidden="true" />
        </span>
        <span>
          <b style={strong}>{group.actor || 'GitStack'}</b> pushed {n} {n === 1 ? 'commit' : 'commits'} referencing this issue · {timeAgo(group.at)}
        </span>
      </div>
      <div className="list" style={{ margin: '-8px 0 18px 48px' }} data-testid="linked-commit-list">
        {group.commits.map((c) => (
          <div key={c.sha} className="list-row small" style={{ padding: '8px 12px' }}>
            <GitCommitHorizontal size={14} className="muted" aria-hidden="true" />
            <span>{c.subject}</span>
            <span className="sp" />
            <Link className="sha mono" to={commitHref(c, repoFullName)}>
              {shortSha(c.sha)}
            </Link>
          </div>
        ))}
      </div>
    </>
  );
}

/** C2: «Closed as completed by commit <sha> on <branch>». */
export function ClosedByCommitEvent({ e, repoFullName, defaultBranch }: { e: IssueEvent; repoFullName: string; defaultBranch: string }) {
  const c = commitOf(e);
  if (!c) return null;
  const branch = branchName(c.ref) || defaultBranch;
  return (
    <div className="ev" data-testid="event-closed_by_commit">
      <span className="evi closed">
        <CircleCheck size={14} aria-hidden="true" />
      </span>
      <span>
        Closed as completed by commit{' '}
        <Link className="mono" to={commitHref(c, repoFullName)}>
          {shortSha(c.sha)}
        </Link>{' '}
        on <span className="mono">{branch}</span> · {timeAgo(e.createdAt)}
      </span>
    </div>
  );
}

/** Riquadro «Linked commits» della barra laterale. */
export function LinkedCommitsBox({ commits, repoFullName }: { commits: LinkedCommit[]; repoFullName: string }) {
  return (
    <div className="side-sec" data-testid="linked-commits-box">
      <h4>Linked commits</h4>
      {commits.length === 0 ? (
        <span className="small muted">No linked commits</span>
      ) : (
        <div className="stack" style={{ gap: 6 }}>
          {commits.map((c) => (
            <Link key={c.sha} className="mono small" to={commitHref(c, repoFullName)} title={c.subject}>
              {shortSha(c.sha)} {c.subject}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

/** Conteggio dei commit collegati nella riga della lista issues (mockup 12). */
export function LinkedCommitsCount({ count }: { count: number }) {
  if (!count) return null;
  return (
    <>
      {' · '}
      <span data-testid="linked-commit-count">
        <GitCommitHorizontal size={13} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> {count} linked {count === 1 ? 'commit' : 'commits'}
      </span>
    </>
  );
}
