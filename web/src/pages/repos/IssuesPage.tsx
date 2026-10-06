import { CircleCheck, CircleDot, CircleSlash, Flag, MessageSquare, Plus, Search, Tag as TagIcon } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ErrorAlert } from '../../components';
import { timeAgo } from '../../lib/format';
import { DEFAULT_QUERY, hasFreeText, qualifierValues, setQualifier, withTrailingSpace } from '../../lib/issueQuery';
import type { Qualifier } from '../../lib/issueQuery';
import { countIssues, fetchIssues, fetchLabels, fetchMilestones } from '../../lib/issuesApi';
import type { IssueList, IssueSort, IssueSummary, IssueUser, Label, Milestone } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { LinkedCommitsCount } from './IssueLinks';
import { useLoad } from '../../lib/useLoad';

const SORTS: { value: IssueSort; label: string }[] = [
  { value: 'created', label: 'Newest' },
  { value: 'updated', label: 'Recently updated' },
  { value: 'comments', label: 'Most commented' },
  { value: 'relevance', label: 'Best match' },
];

function parseSort(v: string | null): IssueSort | '' {
  return SORTS.some((s) => s.value === v) ? (v as IssueSort) : '';
}

/**
 * Lista issues del repo (mockup 12, I10). L'indirizzo e' la fonte di verita':
 * `?q=` e' la ricerca (default `is:open`), `?sort=` l'ordinamento. Filtri e
 * schede Open/Closed riscrivono la barra, e la barra riscrive l'indirizzo.
 */
export function IssuesPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [params, setParams] = useSearchParams();
  const q = params.get('q') ?? DEFAULT_QUERY;
  const sort = parseSort(params.get('sort'));
  const base = `/${owner}/${name}`;

  const go = (nextQ: string, nextSort: IssueSort | '' = sort) => {
    const p = new URLSearchParams();
    p.set('q', nextQ.trim());
    if (nextSort && (nextSort !== 'relevance' || hasFreeText(nextQ))) p.set('sort', nextSort);
    setParams(p);
  };

  return (
    <div className="stack" style={{ maxWidth: 1040 }}>
      <IssuesBody key={`${q}|${sort}`} owner={owner} name={name} base={base} q={q} sort={sort} go={go} />
    </div>
  );
}

interface BodyProps {
  owner: string;
  name: string;
  base: string;
  q: string;
  sort: IssueSort | '';
  go: (q: string, sort?: IssueSort | '') => void;
}

function IssuesBody({ owner, name, base, q, sort, go }: BodyProps) {
  const list = useLoad(() => fetchIssues(owner, name, q, sort));
  const labels = useLoad(() => fetchLabels(owner, name).catch(() => ({ items: [] as Label[], total: 0 })));
  const milestones = useLoad(() => fetchMilestones(owner, name).catch(() => ({ items: [] as Milestone[], total: 0 })));
  const rest = useMemo(() => setQualifier(setQualifier(q, 'is', null), 'reason', null), [q]);
  const counts = useLoad(() =>
    Promise.all([countIssues(owner, name, `${rest} is:open`.trim()), countIssues(owner, name, `${rest} is:closed`.trim())]).catch(() => null),
  );

  const [text, setText] = useState(withTrailingSpace(q));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    go(text);
  };

  const closedView = qualifierValues(q, 'is').includes('closed');
  const openTotal = counts.data?.[0];
  const closedTotal = counts.data?.[1];
  const setFilter = (key: Qualifier, value: string | null) => go(setQualifier(q, key, value));
  const labelCount = labels.data?.total;
  const milestoneCount = milestones.data?.total;

  return (
    <>
      <form className="row" style={{ flexWrap: 'nowrap' }} role="search" onSubmit={submit}>
        <div className="input-icon" style={{ flex: 1 }}>
          <Search size={16} aria-hidden="true" />
          <input className="input mono" style={{ fontSize: 13 }} aria-label="Search issues" value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} />
        </div>
        <Link className="btn" to={`${base}/labels`}>
          <TagIcon size={16} aria-hidden="true" />
          Labels {labelCount !== undefined ? <span className="counter">{labelCount}</span> : null}
        </Link>
        <Link className="btn" to={`${base}/milestones`}>
          <Flag size={16} aria-hidden="true" />
          Milestones {milestoneCount !== undefined ? <span className="counter">{milestoneCount}</span> : null}
        </Link>
        <Link className="btn btn-primary" to={`${base}/issues/new`}>
          <Plus size={16} aria-hidden="true" />
          New issue
        </Link>
      </form>

      <div className="list ilist">
        <div className="list-row list-head" style={{ flexWrap: 'wrap' }}>
          <span className="seg" role="group" aria-label="State">
            <button type="button" className={!closedView ? 'active' : undefined} aria-pressed={!closedView} onClick={() => go(setQualifier(setQualifier(q, 'reason', null), 'is', 'open'))}>
              <CircleDot size={14} aria-hidden="true" /> Open{openTotal !== undefined ? ` ${openTotal}` : ''}
            </button>
            <button type="button" className={closedView ? 'active' : undefined} aria-pressed={closedView} onClick={() => go(setQualifier(q, 'is', 'closed'))}>
              <CircleCheck size={14} aria-hidden="true" /> Closed{closedTotal !== undefined ? ` ${closedTotal}` : ''}
            </button>
          </span>
          <span className="sp" />
          <Filters q={q} sort={sort} items={list.data?.items ?? []} labels={labels.data?.items ?? []} milestones={milestones.data?.items ?? []} setFilter={setFilter} go={go} />
        </div>
        <Rows list={list.data} loading={list.loading} error={list.error} base={base} />
      </div>

      <div className="row small muted" style={{ justifyContent: 'center', gap: 18 }}>
        <span className="row" style={{ gap: 5 }}>
          <CircleCheck size={14} aria-hidden="true" style={{ color: 'var(--closed)' }} />
          Closed as completed <span className="mono">reason:completed</span>
        </span>
        <span className="row" style={{ gap: 5 }}>
          <CircleSlash size={14} aria-hidden="true" style={{ color: 'var(--text-subtle)' }} />
          Closed as not planned or duplicate <span className="mono">reason:not-planned</span> <span className="mono">reason:duplicate</span>
        </span>
      </div>
      <p className="small muted" style={{ textAlign: 'center' }}>
        Tip: <span className="mono">assignee:@agents</span> shows issues assigned to coding agents. Same filters work with <span className="mono">gs issue list --search</span>.
      </p>
    </>
  );
}

const uniqueUsers = (users: IssueUser[]): string[] => [...new Set(users.map((u) => u.username))].sort();

interface FiltersProps {
  q: string;
  sort: IssueSort | '';
  items: IssueSummary[];
  labels: Label[];
  milestones: Milestone[];
  setFilter: (key: Qualifier, value: string | null) => void;
  go: (q: string, sort?: IssueSort | '') => void;
}

function Filters({ q, sort, items, labels, milestones, setFilter, go }: FiltersProps) {
  const current = (key: Qualifier) => qualifierValues(q, key)[0] ?? '';
  const authors = uniqueUsers([...items.map((i) => i.author)]);
  const assignees = uniqueUsers(items.flatMap((i) => i.assignees));
  const withCurrent = (opts: string[], cur: string) => (cur && !opts.includes(cur) ? [...opts, cur] : opts);

  // `no:` e' un valore a parte: «none» scrive no:<campo>, gli altri qualificatori lo tolgono.
  const noOf = (field: string) => qualifierValues(q, 'no').includes(field);
  const pick = (key: Qualifier, noField: string | null, value: string) => {
    let next = setQualifier(q, key, null);
    if (noField) next = setQualifier(next, 'no', null);
    if (value === '__none') next = setQualifier(next, 'no', noField);
    else if (value) next = setQualifier(next, key, value);
    go(next);
  };

  return (
    <span className="row" style={{ gap: 10, flexWrap: 'wrap' }}>
      <select className="select" style={{ width: "auto" }} aria-label="Author" value={current('author')} onChange={(e) => setFilter('author', e.target.value || null)}>
        <option value="">Author</option>
        {withCurrent(authors, current('author')).map((u) => (
          <option key={u} value={u}>
            {u}
          </option>
        ))}
      </select>
      <select className="select" style={{ width: "auto" }} aria-label="Label" value={noOf('label') ? '__none' : current('label')} onChange={(e) => pick('label', 'label', e.target.value)}>
        <option value="">Label</option>
        <option value="__none">Unlabeled</option>
        {withCurrent(labels.map((l) => l.name), current('label')).map((n) => (
          <option key={n} value={n}>
            {n}
          </option>
        ))}
      </select>
      <select className="select" style={{ width: "auto" }} aria-label="Milestone" value={noOf('milestone') ? '__none' : current('milestone')} onChange={(e) => pick('milestone', 'milestone', e.target.value)}>
        <option value="">Milestone</option>
        <option value="__none">No milestone</option>
        {withCurrent(milestones.map((m) => m.title), current('milestone')).map((t) => (
          <option key={t} value={t}>
            {t}
          </option>
        ))}
      </select>
      <select className="select" style={{ width: "auto" }} aria-label="Assignee" value={noOf('assignee') ? '__none' : current('assignee')} onChange={(e) => pick('assignee', 'assignee', e.target.value)}>
        <option value="">Assignee</option>
        <option value="__none">Unassigned</option>
        <option value="@me">Assigned to me</option>
        <option value="@agents">Assigned to agents</option>
        {withCurrent(assignees, current('assignee')).map((u) => (
          <option key={u} value={u}>
            {u}
          </option>
        ))}
      </select>
      <select className="select" style={{ width: "auto" }} aria-label="Sort" value={sort} onChange={(e) => go(q, parseSort(e.target.value))}>
        <option value="">Sort</option>
        {SORTS.filter((s) => s.value !== 'relevance' || hasFreeText(q)).map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </select>
    </span>
  );
}

function Rows({ list, loading, error, base }: { list: IssueList | null; loading: boolean; error: string | null; base: string }) {
  if (error) return <div style={{ padding: 16 }}><ErrorAlert message={error} /></div>;
  if (!list || loading) return <p className="muted" style={{ padding: 16 }}>Loading issues…</p>;
  if (list.items.length === 0) return <p className="muted" style={{ padding: 16 }}>No issues match your search.</p>;
  return (
    <>
      {list.items.map((i) => (
        <IssueRow key={i.number} issue={i} base={base} />
      ))}
    </>
  );
}

/** Icona di stato (I2): aperta, chiusa come completata, chiusa come scartata/duplicata. */
export function StateIcon({ issue }: { issue: Pick<IssueSummary, 'state' | 'closeReason'> }) {
  const style = { marginTop: 3, flexShrink: 0 } as const;
  if (issue.state === 'open') return <CircleDot size={16} aria-label="Open" role="img" style={{ ...style, color: 'var(--success)' }} />;
  if (issue.closeReason === 'not_planned' || issue.closeReason === 'duplicate') {
    const label = issue.closeReason === 'duplicate' ? 'Closed as duplicate' : 'Closed as not planned';
    return <CircleSlash size={16} aria-label={label} role="img" style={{ ...style, color: 'var(--text-subtle)' }} />;
  }
  return <CircleCheck size={16} aria-label="Closed as completed" role="img" style={{ ...style, color: 'var(--closed)' }} />;
}

function Avatar({ user }: { user: IssueUser }) {
  const initials = (user.displayName || user.username).slice(0, 2).toUpperCase();
  return (
    <span className="row" style={{ gap: 4 }} title={user.kind === 'agent' ? `${user.username} (agent)` : user.username}>
      <span className="avatar" aria-label={user.username} role="img">
        {initials}
      </span>
      {user.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null}
    </span>
  );
}

function IssueRow({ issue, base }: { issue: IssueSummary; base: string }) {
  return (
    <div className="list-row hover" style={{ alignItems: 'flex-start' }} data-testid={`issue-${issue.number}`}>
      <StateIcon issue={issue} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <Link className="ititle" to={`${base}/issues/${issue.number}`}>
          {issue.title}
        </Link>{' '}
        {issue.labels.map((l) => (
          <span key={l.id} className="label" style={{ '--lc': `#${l.color}` } as React.CSSProperties}>
            {l.name}
          </span>
        ))}
        <div className="imeta">
          #{issue.number} {issue.state === 'open' ? 'opened' : 'closed'} <span title={issue.createdAt}>{timeAgo(issue.state === 'open' ? issue.createdAt : issue.updatedAt)}</span> by {issue.author.username}
          {issue.milestone ? (
            <>
              {' · '}
              <Flag size={13} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> {issue.milestone.title}
            </>
          ) : null}
          <LinkedCommitsCount count={(issue as IssueSummary & { linkedCommitCount?: number }).linkedCommitCount ?? 0} />
        </div>
      </div>
      <span className="iright row" style={{ gap: 8 }}>
        {issue.assignees.map((a) => (
          <Avatar key={a.id} user={a} />
        ))}
        <span style={{ minWidth: 34 }} aria-label={`${issue.commentCount} comments`} role="img">
          <MessageSquare size={14} aria-hidden="true" /> {issue.commentCount}
        </span>
      </span>
    </div>
  );
}
