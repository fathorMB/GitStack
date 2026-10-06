import { Search } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert } from '../../components';
import { blobLineHref, searchCode, searchHref } from '../../lib/codeApi';
import type { CodeSearchHit } from '../../lib/codeApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

/** Risultati di Search code (B5): /<owner>/<repo>/search?q=&ref=. */
export function SearchPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [params] = useSearchParams();
  const q = (params.get('q') ?? '').trim();
  const ref = params.get('ref') || repo.defaultBranch || 'main';
  const navigate = useNavigate();
  const [draft, setDraft] = useState(q);
  function submit(e: FormEvent) {
    e.preventDefault();
    const next = draft.trim();
    if (next !== '') navigate(searchHref(owner, name, next, ref));
  }
  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <form className="row" role="search" onSubmit={submit}>
        <div className="input-icon grow">
          <Search size={16} aria-hidden="true" />
          <input className="input" aria-label="Search query" value={draft} onChange={(e) => setDraft(e.target.value)} />
        </div>
        <Button type="submit" variant="primary">
          Search
        </Button>
      </form>
      {q.length < 2 ? <p className="muted">Type at least 2 characters to search the code.</p> : <Results key={`${q}\u0000${ref}`} owner={owner} repo={name} q={q} refName={ref} />}
    </div>
  );
}

function Results({ owner, repo, q, refName }: { owner: string; repo: string; q: string; refName: string }) {
  const { data, error } = useLoad(() => searchCode(owner, repo, q, refName));
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Searching…</p>;
  const byPath = new Map<string, CodeSearchHit[]>();
  for (const h of data.results) byPath.set(h.path, [...(byPath.get(h.path) ?? []), h]);
  return (
    <>
      <p className="muted small">
        {data.results.length} {data.results.length === 1 ? 'result' : 'results'} for <b>{q}</b> on <span className="mono">{refName}</span>
      </p>
      {data.limitReached ? (
        <div className="alert alert-info" role="status">
          Results limited to the first {data.results.length}: there are more matches. Refine your search to narrow them down.
        </div>
      ) : null}
      {data.timedOut ? (
        <div className="alert alert-info" role="status">
          The search timed out: the results are partial.
        </div>
      ) : null}
      {data.results.length === 0 ? <EmptyState icon={<Search size={32} aria-hidden="true" />} title={`No results for “${q}”`} description="Try a different search term or another branch or tag." /> : null}
      {[...byPath].map(([path, hits]) => (
        <section key={path} className="card" aria-label={path}>
          <div className="card-h mono">{path}</div>
          <ul className="list" style={{ listStyle: 'none', padding: 0, margin: 0 }}>
            {hits.map((h) => (
              <li key={h.line} className="list-row">
                <Link className="mono small muted" to={blobLineHref(owner, repo, refName, h.path, h.line)} aria-label={`${h.path} line ${h.line}`} style={{ minWidth: 48 }}>
                  {h.line}
                </Link>
                <code className="mono small" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  <Highlighted text={h.fragment} q={q} />
                </code>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </>
  );
}

/** Evidenzia le occorrenze di q (senza distinguere maiuscole) con <mark>. */
export function Highlighted({ text, q }: { text: string; q: string }) {
  const lower = text.toLowerCase();
  const needle = q.toLowerCase();
  const parts: ReactNode[] = [];
  let from = 0;
  for (let i = lower.indexOf(needle); needle !== '' && i >= 0; i = lower.indexOf(needle, from)) {
    if (i > from) parts.push(text.slice(from, i));
    parts.push(<mark key={i}>{text.slice(i, i + needle.length)}</mark>);
    from = i + needle.length;
  }
  parts.push(text.slice(from));
  return <>{parts}</>;
}
