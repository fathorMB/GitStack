import { Bot, GitBranch, MessageSquare, Plus, Search, Info } from 'lucide-react';
import { useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ErrorAlert } from '../../components';
import { timeAgo } from '../../lib/format';
import { hasFreeText, qualifierValues, setQualifier, withTrailingSpace } from '../../lib/issueQuery';
import type { Qualifier } from '../../lib/issueQuery';
import { countPulls, fetchPulls } from '../../lib/pullsApi';
import type { PullRequestList, PullRequestSummary } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

type PullSort = 'created' | 'updated' | 'comments';
const SORTS: { value: PullSort; label: string }[] = [
  { value: 'created', label: 'Newest' },
  { value: 'updated', label: 'Recently updated' },
  { value: 'comments', label: 'Most commented' },
];
const DEFAULT_PULL_QUERY = 'is:open';
const parseSort = (v: string | null): PullSort | '' => (SORTS.some((s) => s.value === v) ? (v as PullSort) : '');

/**
 * Lista delle Pull Request (mockup 25, PR10). Come le issues l'indirizzo e' la
 * fonte di verita': `?q=` e' la ricerca (`is:pr` e' implicito), `?sort=` l'ordine.
 */
export function PullsPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [params, setParams] = useSearchParams();
  const q = params.get('q') ?? DEFAULT_PULL_QUERY;
  const sort = parseSort(params.get('sort'));
  const go = (nextQ: string, nextSort: PullSort | '' = sort) => {
    const p = new URLSearchParams();
    p.set('q', nextQ.trim());
    if (nextSort) p.set('sort', nextSort);
    setParams(p);
  };
  return (
    <div className="stack" style={{ maxWidth: 1040 }}>
      <PullsBody key={`${q}|${sort}`} owner={owner} name={name} base={`/${owner}/${name}`} q={q} sort={sort} go={go} />
    </div>
  );
}

interface BodyProps {
  owner: string;
  name: string;
  base: string;
  q: string;
  sort: PullSort | '';
  go: (q: string, sort?: PullSort | '') => void;
}

function PullsBody({ owner, name, base, q, sort, go }: BodyProps) {
  const list = useLoad(() => fetchPulls(owner, name, q, sort));
  const rest = setQualifier(q, 'is', null);
  const counts = useLoad(() =>
    Promise.all(['open', 'merged', 'closed'].map((s) => countPulls(owner, name, `${rest} is:${s}`.trim()))).catch(() => null),
  );
  const [text, setText] = useState(withTrailingSpace(q));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    go(text);
  };
  const view = qualifierValues(q, 'is').find((v) => v === 'merged' || v === 'closed') ?? 'open';
  const tab = (state: 'open' | 'merged' | 'closed', label: string, n: number | undefined) => (
    <button type="button" className={view === state ? 'active' : undefined} aria-pressed={view === state} onClick={() => go(setQualifier(q, 'is', state))}>
      <GitBranch size={14} aria-hidden="true" /> {label}
      {n !== undefined ? ` ${n}` : ''}
    </button>
  );
  const setFilter = (key: Qualifier, value: string | null) => go(setQualifier(q, key, value));
  const current = (key: Qualifier) => qualifierValues(q, key)[0] ?? '';
  const authors = [...new Set((list.data?.items ?? []).map((i) => i.author.username))].sort();
  const withCurrent = (opts: string[], cur: string) => (cur && !opts.includes(cur) ? [...opts, cur] : opts);

  return (
    <>
      <form className="row" style={{ flexWrap: 'nowrap' }} role="search" onSubmit={submit}>
        <div className="input-icon" style={{ flex: 1 }}>
          <Search size={16} aria-hidden="true" />
          <input className="input mono" style={{ fontSize: 13 }} aria-label="Search pull requests" value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} />
        </div>
        <Link className="btn btn-primary" to={`${base}/pulls/new`}>
          <Plus size={16} aria-hidden="true" />
          New pull request
        </Link>
      </form>

      <div className="list ilist">
        <div className="list-row list-head" style={{ flexWrap: 'wrap' }}>
          <span className="seg" role="group" aria-label="State">
            {tab('open', 'Open', counts.data?.[0])}
            {tab('merged', 'Merged', counts.data?.[1])}
            {tab('closed', 'Closed', counts.data?.[2])}
          </span>
          <span className="sp" />
          <span className="row" style={{ gap: 10, flexWrap: 'wrap' }}>
            <select className="select" style={{ width: 'auto' }} aria-label="Author" value={current('author')} onChange={(e) => setFilter('author', e.target.value || null)}>
              <option value="">Author</option>
              <option value="@me">Created by me</option>
              <option value="@agents">Created by agents</option>
              {withCurrent(authors, current('author')).map((u) => (
                <option key={u} value={u}>
                  {u}
                </option>
              ))}
            </select>
            <select className="select" style={{ width: 'auto' }} aria-label="Reviews" value={current('review')} onChange={(e) => setFilter('review', e.target.value || null)}>
              <option value="">Reviews</option>
              <option value="required">Review required</option>
              <option value="approved">Approved</option>
              <option value="changes-requested">Changes requested</option>
            </select>
            <select className="select" style={{ width: 'auto' }} aria-label="Draft" value={qualifierValues(q, 'is').includes('draft') ? 'draft' : ''} onChange={(e) => go(e.target.value ? `${q} is:draft` : q.split(/\s+/).filter((t) => t !== 'is:draft').join(' '))}>
              <option value="">Draft</option>
              <option value="draft">Drafts only</option>
            </select>
            <select className="select" style={{ width: 'auto' }} aria-label="Sort" value={sort} onChange={(e) => go(q, parseSort(e.target.value))}>
              <option value="">Sort</option>
              {SORTS.filter((s) => s.value !== 'created' || true).map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </span>
        </div>
        <Rows list={list.data} loading={list.loading} error={list.error} base={base} freeText={hasFreeText(q)} />
      </div>

      <p className="small muted" style={{ textAlign: 'center' }}>
        <Info size={14} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> Same search syntax as issues: <span className="mono">is:draft</span>, <span className="mono">review:required</span>,{' '}
        <span className="mono">author:@agents</span>. Agents use <span className="mono">gs pr list --json</span>.
      </p>
    </>
  );
}

function Rows({ list, loading, error, base, freeText }: { list: PullRequestList | null; loading: boolean; error: string | null; base: string; freeText: boolean }) {
  if (error)
    return (
      <div style={{ padding: 16 }}>
        <ErrorAlert message={error} />
      </div>
    );
  if (!list || loading) return <p className="muted" style={{ padding: 16 }}>Loading pull requests…</p>;
  if (list.items.length === 0) return <p className="muted" style={{ padding: 16 }}>{freeText ? 'No pull requests match your search.' : 'No pull requests here.'}</p>;
  return (
    <>
      {list.items.map((p) => (
        <PullRow key={p.number} pr={p} base={base} />
      ))}
    </>
  );
}

const stateStyle = (pr: PullRequestSummary): CSSProperties => ({
  marginTop: 3,
  flexShrink: 0,
  color: pr.state === 'merged' ? 'var(--accent)' : pr.state === 'closed' ? 'var(--closed)' : pr.draft ? 'var(--text-subtle)' : 'var(--success)',
});

function Reviews({ pr }: { pr: PullRequestSummary }) {
  if (pr.state !== 'open' || pr.draft) return null;
  if (pr.reviewDecision === 'approved') return <span className="pr-ok">✓ Approved</span>;
  if (pr.reviewDecision === 'changes_requested') return <span className="pr-ko">Changes requested</span>;
  if (pr.reviewDecision === 'required') return <span className="muted">Review required</span>;
  return null;
}

function Checks({ pr }: { pr: PullRequestSummary }) {
  if (pr.state !== 'open' || !pr.statusState || pr.statusState === 'none') return null;
  if (pr.statusState === 'success') return <span className="pr-ok">✓ Checks passed</span>;
  if (pr.statusState === 'pending') return <span className="muted">◌ checks running</span>;
  return <span className="pr-ko">✗ Checks failed</span>;
}

function PullRow({ pr, base }: { pr: PullRequestSummary; base: string }) {
  const stateLabel = pr.state === 'merged' ? 'Merged' : pr.state === 'closed' ? 'Closed' : pr.draft ? 'Draft' : 'Open';
  const when = pr.state === 'merged' && pr.mergedAt ? `merged ${timeAgo(pr.mergedAt)}` : pr.state === 'closed' && pr.closedAt ? `closed ${timeAgo(pr.closedAt)}` : `opened ${timeAgo(pr.createdAt)}`;
  const parts = [<Reviews key="r" pr={pr} />, <Checks key="c" pr={pr} />].filter((e) => e.type !== null);
  return (
    <div className="list-row hover" style={{ alignItems: 'flex-start' }} data-testid={`pull-${pr.number}`}>
      <GitBranch size={16} aria-label={stateLabel} role="img" style={stateStyle(pr)} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <Link className="ititle" to={`${base}/pulls/${pr.number}`}>
          {pr.title}
        </Link>{' '}
        {pr.draft && pr.state === 'open' ? <span className="badge">Draft</span> : null}
        <div className="imeta">
          #{pr.number} {when} by {pr.author.username} {pr.author.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null}
          {' · '}
          <span className="branch-chip">{pr.sourceBranch}</span> → <span className="branch-chip">{pr.targetBranch}</span>
          {parts.map((p, i) => (
            <span key={i}>
              {' · '}
              {p}
            </span>
          ))}
        </div>
      </div>
      <span className="iright row" style={{ gap: 8 }}>
        {pr.author.kind === 'agent' ? (
          <span className="avatar av-bot" role="img" aria-label={pr.author.username}>
            <Bot size={14} />
          </span>
        ) : (
          <span className="avatar" role="img" aria-label={pr.author.username}>
            {pr.author.username.slice(0, 2).toUpperCase()}
          </span>
        )}
        <span style={{ minWidth: 34 }} aria-label={`${pr.commentCount} comments`} role="img">
          {pr.commentCount > 0 ? (
            <>
              <MessageSquare size={14} aria-hidden="true" /> {pr.commentCount}
            </>
          ) : null}
        </span>
      </span>
    </div>
  );
}
