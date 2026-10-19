import { ChevronDown, FileText, MessageSquare } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Button, Checkbox, ErrorAlert } from '../../components';
import { ReviewDiff } from '../../components/ReviewDiff';
import type { LineCommentInput } from '../../components/ReviewDiff';
import { ApiError, describeError } from '../../lib/http';
import { fetchMyRole } from '../../lib/issuesApi';
import {
  fetchPull,
  fetchPullFiles,
  fetchPullThreads,
  postLineComment,
  setFileViewed,
  setThreadResolved,
  submitReview,
} from '../../lib/pullsApi';
import type { PullRequest, PullRequestFiles, PullRequestThread, ReviewEvent } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { loadMe } from './repoAdmin';

interface Me {
  username: string;
  kind: 'human' | 'agent';
  isAdmin: boolean;
}

/** Errore leggibile; il backend di review puo' ancora rispondere 501. */
function reviewError(err: unknown): string {
  if (err instanceof ApiError && err.status === 501) return 'Reviews are not available on this server yet (501 Not Implemented).';
  return describeError(err);
}

const EVENTS: { value: ReviewEvent; label: string; hint: string }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without explicit approval.' },
  { value: 'approve', label: 'Approve', hint: 'Give your approval to merge. Counts only for people with Write, not for the author.' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback that must be addressed before merging.' },
];

/** Dettaglio Pull Request, scheda Files changed (mockup 27, PR5, PR12, B6). */
export function PullFilesPage({ repo }: { repo: Repository }) {
  const { number: raw = '' } = useParams();
  const number = Number(raw);
  const owner = repo.owner.name;
  const name = repo.name;
  const ref = useMemo(() => ({ owner, repo: name, number }), [owner, name, number]);

  const [pr, setPr] = useState<PullRequest | null>(null);
  const [files, setFiles] = useState<PullRequestFiles | null>(null);
  const [threads, setThreads] = useState<PullRequestThread[]>([]);
  const [viewed, setViewed] = useState<Set<string>>(new Set());
  const [me, setMe] = useState<Me | null>(null);
  const [canWrite, setCanWrite] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [mode, setMode] = useState<'unified' | 'split'>('unified');
  const [ignoreWs, setIgnoreWs] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);

  const loadThreads = useCallback(async () => {
    setThreads(await fetchPullThreads(ref));
  }, [ref]);

  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const [p, role, who] = await Promise.all([fetchPull(ref), fetchMyRole(repo.id).catch(() => null), loadMe().catch(() => null)]);
        if (!live) return;
        setPr(p);
        setMe(who ? { username: who.username, kind: who.kind, isAdmin: who.isAdmin === true } : null);
        setCanWrite(role === 'write' || role === 'admin' || who?.isAdmin === true);
      } catch (err) {
        if (live) setLoadError(describeError(err));
      }
    })();
    return () => {
      live = false;
    };
  }, [ref, repo.id]);

  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const f = await fetchPullFiles(ref, ignoreWs);
        if (!live) return;
        setFiles(f);
        setViewed(new Set(f.viewedPaths));
      } catch (err) {
        if (live) setLoadError(reviewError(err));
      }
    })();
    return () => {
      live = false;
    };
  }, [ref, ignoreWs]);

  useEffect(() => {
    // Senza conversazioni (o con 501) il diff si legge comunque.
    fetchPullThreads(ref).then(setThreads, () => setThreads([]));
  }, [ref]);

  const run = useCallback(
    async (fn: () => Promise<unknown>): Promise<boolean> => {
      setActionError(null);
      setNotice(null);
      try {
        await fn();
        return true;
      } catch (err) {
        setActionError(reviewError(err));
        return false;
      }
    },
    [],
  );

  if (!Number.isInteger(number) || number < 1) return <ErrorAlert message="Pull request not found." />;
  if (loadError && !(pr && files)) return <ErrorAlert message={loadError} />;
  if (!pr || !files) return <p className="muted">Loading changes…</p>;

  const base = `/${owner}/${name}`;
  const isAuthor = me !== null && me.username === pr.author.username;
  const canComment = pr.state === 'open' && !repo.archived;
  const canResolveThreads = (canWrite || isAuthor) && !repo.archived;
  const resolvedCount = threads.filter((t) => t.resolved).length;

  const onViewed = (path: string, v: boolean) => {
    const prev = viewed;
    setViewed(new Set(v ? [...prev, path] : [...prev].filter((p) => p !== path)));
    void run(() => setFileViewed(ref, path, v)).then((ok) => {
      if (!ok) setViewed(prev);
    });
  };

  const onComment = (c: LineCommentInput) =>
    run(async () => {
      await postLineComment(ref, { ...c, commitSha: files.headSha });
      await loadThreads();
    });

  const onReply = (threadId: string, body: string) =>
    run(async () => {
      await postLineComment(ref, { body, inReplyTo: threadId });
      await loadThreads();
    });

  const onResolve = (threadId: string, resolved: boolean) => {
    void run(async () => {
      await setThreadResolved(ref, threadId, resolved);
      await loadThreads();
    });
  };

  return (
    <div style={{ maxWidth: 1100 }}>
      <h1 className="issue-title" style={{ fontSize: 20 }}>
        {pr.title} <span>#{pr.number}</span>
      </h1>
      <nav className="tabs" aria-label="Pull request sections" style={{ marginBottom: 14 }}>
        <Link className="tab" to={`${base}/pulls/${pr.number}`}>
          <MessageSquare size={16} aria-hidden="true" />
          Conversation <span className="counter">{pr.commentCount}</span>
        </Link>
        <span className="tab active" aria-current="page">
          <FileText size={16} aria-hidden="true" />
          Files changed <span className="counter">{files.filesChanged}</span>
        </span>
      </nav>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {notice ? (
        <p className="small" role="status" style={{ color: 'var(--success)' }}>
          {notice}
        </p>
      ) : null}

      <div className="row" style={{ marginBottom: 12, position: 'relative', flexWrap: 'wrap' }}>
        <span className="muted">
          <b style={{ color: 'var(--text)' }}>
            {files.filesChanged} {files.filesChanged === 1 ? 'file' : 'files'}
          </b>{' '}
          · <b style={{ color: 'var(--success)' }}>+{files.additions}</b> <b style={{ color: 'var(--danger)' }}>−{files.deletions}</b> · {resolvedCount} of {threads.length}{' '}
          {threads.length === 1 ? 'conversation' : 'conversations'} resolved
        </span>
        <span className="sp" style={{ flex: 1 }} />
        <Checkbox label="Ignore whitespace" checked={ignoreWs} onCheckedChange={setIgnoreWs} />
        <span className="seg" role="group" aria-label="Diff layout">
          <button type="button" className={mode === 'unified' ? 'active' : ''} aria-pressed={mode === 'unified'} onClick={() => setMode('unified')}>
            Unified
          </button>
          <button type="button" className={mode === 'split' ? 'active' : ''} aria-pressed={mode === 'split'} onClick={() => setMode('split')}>
            Split
          </button>
        </span>
        <Button variant="primary" size="sm" aria-expanded={reviewOpen} aria-haspopup="dialog" onClick={() => setReviewOpen((v) => !v)}>
          Review changes
          <ChevronDown size={14} aria-hidden="true" />
        </Button>
        {reviewOpen ? (
          <ReviewPanel
            me={me}
            isAuthor={isAuthor}
            canReview={pr.state === 'open' && !repo.archived}
            onSubmit={async (event, body) => {
              const ok = await run(async () => {
                await submitReview(ref, event, body, files.headSha);
                await loadThreads();
              });
              if (ok) {
                setReviewOpen(false);
                setNotice('Review submitted.');
              }
              return ok;
            }}
          />
        ) : null}
      </div>

      {files.listOnly ? (
        <p className="small muted" role="note">
          This pull request is too large to show inline (over 300 files or 20,000 changed lines): only the list of files is shown.
        </p>
      ) : null}
      {files.truncated && !files.listOnly ? <p className="small muted">The diff was truncated: some files are not listed.</p> : null}

      <ReviewDiff
        files={files.files}
        mode={mode}
        listOnly={files.listOnly}
        threads={threads}
        viewed={viewed}
        canComment={canComment}
        canResolve={canResolveThreads}
        onViewed={onViewed}
        onComment={onComment}
        onReply={onReply}
        onResolve={onResolve}
      />
    </div>
  );
}

function ReviewPanel({
  me,
  isAuthor,
  canReview,
  onSubmit,
}: {
  me: Me | null;
  isAuthor: boolean;
  canReview: boolean;
  onSubmit: (event: ReviewEvent, body: string) => Promise<boolean>;
}) {
  const [event, setEvent] = useState<ReviewEvent>(isAuthor ? 'comment' : 'approve');
  const [body, setBody] = useState('');
  const [busy, setBusy] = useState(false);
  const isAgent = me?.kind === 'agent';
  const needsBody = event === 'comment' && !body.trim();

  return (
    <div className="menu review-pop" role="dialog" aria-label="Finish your review">
      <div className="small" style={{ fontWeight: 600, marginBottom: 6 }}>
        Finish your review
      </div>
      <textarea className="textarea" style={{ minHeight: 70 }} aria-label="Review summary" placeholder="Leave a summary (optional)" value={body} onChange={(e) => setBody(e.target.value)} />
      {EVENTS.map((o, i) => {
        const disabled = o.value === 'approve' && isAuthor;
        return (
          <label className="check small" key={o.value} style={{ marginTop: i === 0 ? 8 : 6 }}>
            <input type="radio" name="review-event" value={o.value} checked={event === o.value} disabled={disabled} onChange={() => setEvent(o.value)} />
            <span>
              <b>{o.label}</b>
              <br />
              <span className="muted">{disabled ? "You can't approve your own pull request." : o.hint}</span>
            </span>
          </label>
        );
      })}
      {event === 'approve' && isAgent ? (
        <p className="small muted" role="note" style={{ marginTop: 8 }}>
          You are an agent: your approval is shown on the pull request but never counts toward the required approvals (PR2).
        </p>
      ) : null}
      {!canReview ? (
        <p className="small muted" role="note" style={{ marginTop: 8 }}>
          This pull request is not open or the repository is archived: reviews are disabled.
        </p>
      ) : null}
      <div className="row" style={{ marginTop: 10 }}>
        <span className="sp" style={{ flex: 1 }} />
        <Button
          variant="primary"
          size="sm"
          disabled={busy || !canReview || needsBody}
          onClick={() => {
            setBusy(true);
            void onSubmit(event, body).finally(() => setBusy(false));
          }}
        >
          Submit review
        </Button>
      </div>
    </div>
  );
}
