import { Bot, Check, FileText, GitBranch, MessageSquare, X } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Button, ErrorAlert, Markdown } from '../../components';
import { timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import { fetchMyRole } from '../../lib/issuesApi';
import type { IssueUser } from '../../lib/issuesApi';
import {
  closePull,
  deletePullBranch,
  dropReviewer,
  fetchCommitStatus,
  fetchMergeability,
  fetchPull,
  fetchPullComments,
  fetchPullReviews,
  markReady,
  mergePull,
  postPullComment,
  reopenPull,
  requestReviewers,
} from '../../lib/pullsApi';
import type { CombinedCommitStatus, PullRequest, PullRequestComment, PullRequestMergeability, PullRequestReview } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { PullMergeBox } from './PullMergeBox';
import { loadMe } from './repoAdmin';

interface Data {
  pr: PullRequest;
  mergeability: PullRequestMergeability | null;
  status: CombinedCommitStatus | null;
  reviews: PullRequestReview[];
  comments: PullRequestComment[];
  canWrite: boolean;
  me: string;
}

/** Dettaglio Pull Request, scheda Conversation (mockup 26, PR1-PR9). */
export function PullDetailPage({ repo }: { repo: Repository }) {
  const { number: raw = '' } = useParams();
  const number = Number(raw);
  const owner = repo.owner.name;
  const name = repo.name;
  const [data, setData] = useState<Data | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const ref = { owner, repo: name, number };
    try {
      const pr = await fetchPull(ref);
      const open = pr.state === 'open';
      const [mergeability, status, reviews, comments, role, me] = await Promise.all([
        open && !pr.draft ? fetchMergeability(ref).catch(() => null) : Promise.resolve(null),
        fetchCommitStatus(owner, name, pr.headSha).catch(() => null),
        fetchPullReviews(ref).catch(() => [] as PullRequestReview[]),
        fetchPullComments(ref).catch(() => [] as PullRequestComment[]),
        fetchMyRole(repo.id).catch(() => null),
        loadMe().catch(() => null),
      ]);
      const write = (role === 'write' || role === 'admin' || me?.isAdmin === true) && !repo.archived;
      setData({ pr, mergeability, status, reviews, comments, canWrite: write, me: me?.username ?? '' });
      setLoadError(null);
    } catch (err) {
      setLoadError(describeError(err));
    }
  }, [owner, name, number, repo.id, repo.archived]);

  useEffect(() => {
    void load();
  }, [load]);

  const act = useCallback(
    async (fn: () => Promise<unknown>) => {
      setActionError(null);
      setBusy(true);
      try {
        await fn();
        await load();
        return true;
      } catch (err) {
        setActionError(describeError(err));
        return false;
      } finally {
        setBusy(false);
      }
    },
    [load],
  );

  if (!Number.isInteger(number) || number < 1) return <ErrorAlert message="Pull request not found." />;
  if (loadError && !data) return <ErrorAlert message={loadError} />;
  if (!data) return <p className="muted">Loading pull request…</p>;

  const ref = { owner, repo: name, number };
  const { pr } = data;
  const isAuthor = data.me !== '' && data.me === pr.author.username;
  const base = `/${owner}/${name}`;

  return (
    <div style={{ maxWidth: 1100 }}>
      <h1 className="issue-title">
        {pr.title} <span>#{pr.number}</span>
      </h1>
      <div className="row" style={{ paddingBottom: 14, marginBottom: 0 }}>
        <StateBadge pr={pr} />
        <span className="muted">
          <b style={{ color: 'var(--text)' }}>{pr.author.username}</b> <AgentBadge user={pr.author} />{' '}
          {pr.state === 'merged' ? 'merged' : 'wants to merge'} {pr.commitCount} {pr.commitCount === 1 ? 'commit' : 'commits'} into <span className="branch-chip">{pr.targetBranch}</span> from{' '}
          <span className="branch-chip">{pr.sourceBranch}</span>
        </span>
      </div>
      <nav className="tabs" aria-label="Pull request sections" style={{ marginBottom: 22 }}>
        <span className="tab active" aria-current="page">
          <MessageSquare size={16} aria-hidden="true" />
          Conversation <span className="counter">{pr.commentCount}</span>
        </span>
        <Link className="tab" to={`${base}/pulls/${pr.number}/files`}>
          <FileText size={16} aria-hidden="true" />
          Files changed <span className="counter">{pr.changedFiles}</span>
        </Link>
      </nav>
      {actionError ? <ErrorAlert message={actionError} /> : null}

      <div className="issue-grid">
        <div className="timeline">
          <div className="cmt">
            <Avatar user={pr.author} />
            <div className="cmt-box">
              <div className="cmt-h">
                <b>{pr.author.username}</b>
                <AgentBadge user={pr.author} />
                <span className="muted">
                  opened {timeAgo(pr.createdAt)}
                  {pr.viaToken ? (
                    <>
                      {' '}
                      via token <span className="mono">{pr.viaToken.name}</span>
                    </>
                  ) : null}
                </span>
                <span className="sp" style={{ flex: 1 }} />
                <span className="badge">Author</span>
              </div>
              <div className="cmt-b">{pr.body.trim() ? <Markdown source={pr.body} repo={{ owner, name }} /> : <span className="muted">No description provided.</span>}</div>
            </div>
          </div>

          <Timeline data={data} />

          <PullMergeBox
            pr={pr}
            mergeability={data.mergeability}
            status={data.status}
            reviews={data.reviews}
            canWrite={data.canWrite}
            isAuthor={isAuthor}
            busy={busy}
            onMerge={(method, del) => void act(() => mergePull(ref, method, del, pr.headSha))}
            onReady={() => void act(() => markReady(ref))}
            onDeleteBranch={() => void act(() => deletePullBranch(ref))}
            onRerequest={(users) => void act(() => requestReviewers(ref, users))}
          />

          <CommentForm
            pr={pr}
            archived={repo.archived}
            canClose={(data.canWrite || isAuthor) && !repo.archived}
            busy={busy}
            onComment={(text) => act(() => postPullComment(ref, text))}
            onClose={() => void act(() => closePull(ref))}
            onReopen={() => void act(() => reopenPull(ref))}
          />
        </div>

        <aside>
          <div className="side-sec">
            <h4>Reviewers</h4>
            <Reviewers data={data} onRemove={(u) => void act(() => dropReviewer(ref, u))} />
          </div>
          <div className="side-sec">
            <h4>Linked issues</h4>
            {pr.closingIssues.length === 0 ? (
              <p className="small muted">None. Use <span className="mono">Fixes #n</span> in the description.</p>
            ) : (
              <>
                <ul className="stack small" style={{ listStyle: 'none', margin: 0, padding: 0, gap: 4 }}>
                  {pr.closingIssues.map((n) => (
                    <li key={n}>
                      <Link to={`${base}/issues/${n}`}>#{n}</Link>
                    </li>
                  ))}
                </ul>
                <p className="small muted" style={{ marginTop: 6 }}>
                  {pr.state === 'merged' ? (
                    <>Closed when this pull request was merged into <span className="mono">{pr.targetBranch}</span>.</>
                  ) : (
                    <>Will be closed when this pull request is merged into <span className="mono">{pr.targetBranch}</span>.</>
                  )}
                </p>
              </>
            )}
          </div>
          <div className="side-sec" style={{ borderBottom: 0 }}>
            <h4>For agents</h4>
            <div className="cmdblock" style={{ fontSize: 11.5, padding: '8px 10px' }}>
              gs pr view {repo.fullName}#{pr.number} --json
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function StateBadge({ pr }: { pr: PullRequest }) {
  if (pr.state === 'merged')
    return (
      <span className="badge badge-merged">
        <GitBranch size={14} aria-hidden="true" /> Merged
      </span>
    );
  if (pr.state === 'closed')
    return (
      <span className="badge badge-closed">
        <X size={14} aria-hidden="true" /> Closed
      </span>
    );
  if (pr.draft)
    return (
      <span className="badge">
        <GitBranch size={14} aria-hidden="true" /> Draft
      </span>
    );
  return (
    <span className="badge badge-open">
      <GitBranch size={14} aria-hidden="true" /> Open
    </span>
  );
}

function Avatar({ user }: { user: IssueUser }) {
  return user.kind === 'agent' ? (
    <span className="avatar avatar-lg av-bot" aria-hidden="true">
      <Bot size={20} />
    </span>
  ) : (
    <span className="avatar avatar-lg" aria-hidden="true">
      {user.username.slice(0, 2).toUpperCase()}
    </span>
  );
}

function AgentBadge({ user }: { user: IssueUser }) {
  return user.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null;
}

const REVIEW_TEXT = { approved: 'approved these changes', changes_requested: 'requested changes', commented: 'reviewed' } as const;

/** Cronologia: commenti, review ed esito (unita/chiusa), in ordine di tempo. */
function Timeline({ data }: { data: Data }) {
  const { pr, reviews, comments } = data;
  const items: { at: string; node: ReactNode }[] = [];

  for (const r of reviews) {
    // Una review di sola approvazione senza testo e' un evento; con testo e' un commento (mockup 26).
    const agentNoCount = r.author.kind === 'agent' && r.state === 'approved';
    if (!r.body.trim() && !agentNoCount) {
      items.push({
        at: r.submittedAt,
        node: (
          <div className="ev" key={`r-${r.id}`}>
            <span className={`evi ${r.state === 'approved' && r.countsAsApproval ? 'ok' : ''}`.trim()}>
              {r.state === 'approved' ? <Check size={14} aria-hidden="true" /> : <X size={14} aria-hidden="true" />}
            </span>
            <span>
              <b style={{ color: 'var(--text)' }}>{r.author.username}</b> {REVIEW_TEXT[r.state]}
              {r.stale ? ' (outdated: new commits were pushed)' : ''} · {timeAgo(r.submittedAt)}
            </span>
          </div>
        ),
      });
      continue;
    }
    items.push({
      at: r.submittedAt,
      node: (
        <div className="cmt" key={`r-${r.id}`}>
          <Avatar user={r.author} />
          <div className="cmt-box">
            <div className="cmt-h">
              <b>{r.author.username}</b>
              <AgentBadge user={r.author} />
              <span className="muted">
                {REVIEW_TEXT[r.state]} · {timeAgo(r.submittedAt)}
              </span>
              <span className="sp" style={{ flex: 1 }} />
              {r.state === 'approved' && !r.countsAsApproval ? (
                <span className="badge" title="Agent approvals are shown but never count (PR2)">
                  {r.stale ? 'Outdated' : "Doesn't count"}
                </span>
              ) : null}
            </div>
            {r.body.trim() ? (
              <div className="cmt-b">
                <Markdown source={r.body} />
              </div>
            ) : null}
          </div>
        </div>
      ),
    });
  }

  for (const c of comments) {
    if (c.deleted) continue;
    items.push({
      at: c.createdAt,
      node: (
        <div className="cmt" key={`c-${c.id}`}>
          <Avatar user={c.author} />
          <div className="cmt-box">
            <div className="cmt-h">
              <b>{c.author.username}</b>
              <AgentBadge user={c.author} />
              <span className="muted">
                commented {timeAgo(c.createdAt)}
                {c.viaToken ? (
                  <>
                    {' '}
                    via token <span className="mono">{c.viaToken.name}</span>
                  </>
                ) : null}
                {c.path ? (
                  <>
                    {' '}
                    on <span className="mono">{c.path}{c.line ? `:${c.line}` : ''}</span>
                  </>
                ) : null}
              </span>
              {c.outdated ? <span className="badge">Outdated</span> : null}
              {c.edited ? <span className="muted small">edited</span> : null}
            </div>
            <div className="cmt-b">
              <Markdown source={c.body} />
            </div>
          </div>
        </div>
      ),
    });
  }

  if (pr.state === 'merged' && pr.mergedAt) {
    items.push({
      at: pr.mergedAt,
      node: (
        <div className="ev" key="merged">
          <span className="evi merged">
            <GitBranch size={14} aria-hidden="true" />
          </span>
          <span>
            <b style={{ color: 'var(--text)' }}>{pr.mergedBy?.username ?? 'Someone'}</b> {pr.mergeMethod === 'squash' ? 'squashed and merged' : 'merged'}
            {pr.mergeCommitSha ? (
              <>
                {' '}
                commit <span className="mono">{pr.mergeCommitSha.slice(0, 7)}</span>
              </>
            ) : null}{' '}
            into <span className="mono">{pr.targetBranch}</span> · {timeAgo(pr.mergedAt)}
          </span>
        </div>
      ),
    });
  } else if (pr.state === 'closed' && pr.closedAt) {
    items.push({
      at: pr.closedAt,
      node: (
        <div className="ev" key="closed">
          <span className="evi closed">
            <X size={14} aria-hidden="true" />
          </span>
          <span>This pull request was closed · {timeAgo(pr.closedAt)}</span>
        </div>
      ),
    });
  }

  items.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  return <>{items.map((i) => i.node)}</>;
}

function Reviewers({ data, onRemove }: { data: Data; onRemove: (username: string) => void }) {
  const { pr, reviews } = data;
  // Ultimo parere di ciascuno; i revisori ancora richiesti senza review sono «in attesa».
  const latest = new Map<string, PullRequestReview>();
  for (const r of [...reviews].sort((a, b) => Date.parse(a.submittedAt) - Date.parse(b.submittedAt))) latest.set(r.author.username, r);
  const people = new Map<string, IssueUser>();
  for (const r of latest.values()) people.set(r.author.username, r.author);
  for (const u of pr.requestedReviewers) people.set(u.username, u);

  if (people.size === 0) return <p className="small muted">No reviews requested.</p>;
  const requested = new Set(pr.requestedReviewers.map((u) => u.username));
  return (
    <ul className="stack" style={{ gap: 6, listStyle: 'none', margin: 0, padding: 0 }}>
      {[...people.values()].map((u) => {
        const r = latest.get(u.username);
        const waiting = requested.has(u.username);
        return (
          <li className="row" key={u.id}>
            <span className={`avatar ${u.kind === 'agent' ? 'av-bot' : ''}`.trim()} aria-hidden="true">
              {u.kind === 'agent' ? <Bot size={14} /> : u.username.slice(0, 2).toUpperCase()}
            </span>
            <b>{u.username}</b>
            <AgentBadge user={u} />
            <span className="sp" style={{ flex: 1 }} />
            {waiting ? (
              <span className="muted small" title="Review requested">
                ◌
              </span>
            ) : r?.state === 'approved' ? (
              <span className={r.countsAsApproval ? 'pr-ok' : 'muted'} title={r.countsAsApproval ? 'Approved' : "Approved: doesn't count"}>
                ✓
              </span>
            ) : r?.state === 'changes_requested' ? (
              <span className="pr-ko" title="Changes requested">
                ✗
              </span>
            ) : null}
            {waiting && data.canWrite && pr.state === 'open' ? (
              <button type="button" className="btn btn-ghost btn-sm" aria-label={`Remove ${u.username} from reviewers`} onClick={() => onRemove(u.username)}>
                <X size={12} aria-hidden="true" />
              </button>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

function CommentForm({
  pr,
  archived,
  canClose,
  busy,
  onComment,
  onClose,
  onReopen,
}: {
  pr: PullRequest;
  archived: boolean;
  canClose: boolean;
  busy: boolean;
  onComment: (text: string) => Promise<boolean>;
  onClose: () => void;
  onReopen: () => void;
}) {
  const [text, setText] = useState('');
  const [preview, setPreview] = useState(false);
  if (archived) return <p className="muted small" role="status">The repository is archived: commenting, closing and reopening are disabled.</p>;

  return (
    <div className="cmt" style={{ marginTop: 18 }}>
      <span className="avatar avatar-lg" aria-hidden="true">
        +
      </span>
      <div className="editor" style={{ flex: 1 }}>
        <div className="tabs" role="tablist">
          <button type="button" role="tab" aria-selected={!preview} className={!preview ? 'tab active' : 'tab'} onClick={() => setPreview(false)}>
            Write
          </button>
          <button type="button" role="tab" aria-selected={preview} className={preview ? 'tab active' : 'tab'} onClick={() => setPreview(true)}>
            Preview
          </button>
        </div>
        {preview ? (
          <div className="editor-preview">{text.trim() ? <Markdown source={text} /> : <span className="muted">Nothing to preview.</span>}</div>
        ) : (
          <textarea
            className="textarea"
            aria-label="Leave a comment"
            placeholder="Leave a comment. Markdown, @mentions and #references supported."
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        )}
        <div className="editor-f">
          Markdown supported
          <span className="sp" style={{ flex: 1 }} />
          {canClose && pr.state === 'open' ? (
            <Button disabled={busy} onClick={onClose}>
              Close pull request
            </Button>
          ) : null}
          {canClose && pr.state === 'closed' ? (
            <Button disabled={busy} onClick={onReopen}>
              Reopen pull request
            </Button>
          ) : null}
          <Button
            variant="primary"
            disabled={busy || !text.trim()}
            onClick={() =>
              void onComment(text).then((ok) => {
                if (ok) setText('');
              })
            }
          >
            Comment
          </Button>
        </div>
      </div>
    </div>
  );
}
