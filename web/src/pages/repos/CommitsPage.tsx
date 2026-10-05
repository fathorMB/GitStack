import { Bot, Check, Copy, GitCommitHorizontal, History, Tag as TagIcon } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button, ErrorAlert } from '../../components';
import { commitHref, commitsHref, fetchBranches, fetchCommits, fetchTags, splitRefPath } from '../../lib/codeApi';
import type { CommitSummary } from '../../lib/codeApi';
import { groupByDay } from '../../lib/commitView';
import { timeAgo } from '../../lib/format';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { RefSwitcher } from './CodeBrowser';
import type { Refs } from './CodeBrowser';

/** Pagina Commits (mockup 09) e History di un file o cartella (B4): `splat` e' `<ref>/<percorso>`. */
export function CommitsPage({ repo, splat }: { repo: Repository; splat: string }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data: refs, error } = useLoad<Refs>(async () => {
    const [branches, tags] = await Promise.all([fetchBranches(owner, name), fetchTags(owner, name)]);
    return { branches, tags };
  });
  if (error) return <ErrorAlert message={error} />;
  if (!refs) return <p className="muted">Loading commits…</p>;
  const defaultRef = repo.defaultBranch || 'main';
  const names = [...refs.branches.items.map((b) => b.name), ...refs.tags.items.map((t) => t.name)];
  const { ref, path } = splat === '' ? { ref: defaultRef, path: '' } : splitRefPath(splat, names);
  return <CommitFilters key={`${ref}\u0000${path}`} repo={repo} refs={refs} refName={ref} path={path} defaultRef={defaultRef} />;
}

function CommitFilters({ repo, refs, refName, path, defaultRef }: { repo: Repository; refs: Refs; refName: string; path: string; defaultRef: string }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [params, setParams] = useSearchParams();
  const author = params.get('author') ?? '';
  const page = Math.max(1, Number(params.get('page')) || 1);
  const [draft, setDraft] = useState(author);

  return (
    <div className="stack" style={{ gap: 0, maxWidth: 1040 }}>
      <div className="row" style={{ marginBottom: 18 }}>
        <RefSwitcher repo={repo} refs={refs} refName={refName} defaultRef={defaultRef} hrefFor={(n) => commitsHref(owner, name, n, path)} />
        {path !== '' ? (
          <span className="row mono small" aria-label="History of">
            <History size={14} aria-hidden="true" />
            <span className="muted">History of</span>
            <b>{path}</b>
            <Link to={commitsHref(owner, name, refName)}>Show all commits</Link>
          </span>
        ) : null}
        <span className="sp" />
        <form
          className="row"
          role="search"
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            const next = new URLSearchParams();
            if (draft.trim()) next.set('author', draft.trim());
            setParams(next);
          }}
        >
          <input className="input" aria-label="Filter by author" placeholder="All authors" value={draft} onChange={(e) => setDraft(e.target.value)} style={{ width: 200 }} />
          <Button size="sm" type="submit">
            Filter
          </Button>
          {author ? (
            <Button
              size="sm"
              type="button"
              onClick={() => {
                setDraft('');
                setParams(new URLSearchParams());
              }}
            >
              Clear filter
            </Button>
          ) : null}
        </form>
      </div>
      <CommitPageList
        key={`${author}\u0000${page}`}
        owner={owner}
        repo={name}
        refName={refName}
        path={path}
        author={author}
        page={page}
        refs={refs}
        onPage={(p) => {
          const next = new URLSearchParams(params);
          if (p > 1) next.set('page', String(p));
          else next.delete('page');
          setParams(next);
        }}
      />
    </div>
  );
}

interface ListProps {
  owner: string;
  repo: string;
  refName: string;
  path: string;
  author: string;
  page: number;
  refs: Refs;
  onPage: (p: number) => void;
}

function CommitPageList({ owner, repo, refName, path, author, page, refs, onPage }: ListProps) {
  const { data, error } = useLoad(() => fetchCommits(owner, repo, { ref: refName, path, author, page }));
  const tagsBySha = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const t of refs.tags.items) m.set(t.commit.sha, [...(m.get(t.commit.sha) ?? []), t.name]);
    return m;
  }, [refs.tags.items]);
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading commits…</p>;
  if (data.items.length === 0) return <p className="muted">{author ? 'No commits by this author.' : 'No commits.'}</p>;
  const groups = groupByDay(data.items);
  return (
    <>
      {groups.map((g, i) => (
        <section key={g.day} aria-label={`Commits on ${g.day}`}>
          <div className="commit-day" style={i > 0 ? { marginTop: 22 } : undefined}>
            <GitCommitHorizontal size={16} aria-hidden="true" />
            Commits on {g.day}
          </div>
          <div className="list">
            {g.commits.map((c) => (
              <CommitRow key={c.sha} owner={owner} repo={repo} commit={c} tags={tagsBySha.get(c.sha) ?? []} />
            ))}
          </div>
        </section>
      ))}
      <nav className="row" style={{ justifyContent: 'center', marginTop: 18 }} aria-label="Pagination">
        <span className="btn-group">
          <Button size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>
            Newer
          </Button>
          <Button size="sm" disabled={!data.hasMore} onClick={() => onPage(page + 1)}>
            Older
          </Button>
        </span>
      </nav>
    </>
  );
}

export function CommitAuthor({ commit }: { commit: CommitSummary }) {
  const agent = commit.author.user?.kind === 'agent';
  return (
    <>
      <span className={`avatar${agent ? ' av-bot' : ''}`} style={{ width: 18, height: 18, fontSize: 8 }} aria-hidden="true">
        {agent ? <Bot size={11} /> : commit.author.name.slice(0, 2).toUpperCase()}
      </span>
      <b style={{ color: 'var(--text)' }}>{commit.author.user?.username ?? commit.author.name}</b>
      {agent ? <span className="badge badge-agent">agent</span> : null}
    </>
  );
}

function CommitRow({ owner, repo, commit, tags }: { owner: string; repo: string; commit: CommitSummary; tags: string[] }) {
  const short = commit.sha.slice(0, 7);
  return (
    <div className="list-row hover">
      <div style={{ flex: 1, minWidth: 0 }}>
        <Link to={commitHref(owner, repo, commit.sha)} style={{ color: 'var(--text)', fontWeight: 600 }}>
          {commit.subject}
        </Link>
        <div className="row small muted" style={{ marginTop: 4 }}>
          <CommitAuthor commit={commit} />
          committed {timeAgo(commit.committer.date)}
        </div>
      </div>
      {tags.map((t) => (
        <span key={t} className="badge">
          <TagIcon size={12} style={{ color: 'var(--success)' }} aria-hidden="true" />
          {t}
        </span>
      ))}
      <Link className="sha" to={commitHref(owner, repo, commit.sha)}>
        {short}
      </Link>
      <CopySha sha={commit.sha} />
    </div>
  );
}

function CopySha({ sha }: { sha: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  async function copy() {
    try {
      await navigator.clipboard.writeText(sha);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }
  return (
    <Button size="sm" variant="ghost" aria-label={copied ? `Copied ${sha.slice(0, 7)}` : `Copy sha ${sha.slice(0, 7)}`} onClick={() => void copy()}>
      {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
    </Button>
  );
}
