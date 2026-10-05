import { Lock, Plus, Search, Shield } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { EmptyState, ErrorAlert } from '../../components';
import { fetchRepos } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

type Filter = 'all' | 'private' | 'internal' | 'archived';
const FILTERS: { id: Filter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'private', label: 'Private' },
  { id: 'internal', label: 'Internal' },
  { id: 'archived', label: 'Archived' },
];

// eslint-disable-next-line react-refresh/only-export-components
export function repoPath(r: Pick<Repository, 'owner' | 'name'>): string {
  return `/${encodeURIComponent(r.owner.name)}/${encodeURIComponent(r.name)}`;
}

export function VisibilityBadge({ visibility }: { visibility: Repository['visibility'] }) {
  return (
    <span className="badge">
      {visibility === 'private' ? <Lock size={12} aria-hidden="true" /> : <Shield size={12} aria-hidden="true" />}
      {visibility === 'private' ? 'Private' : 'Internal'}
    </span>
  );
}

// Elenco dei repo leggibili dall'utente (mockup 03), con filtro per
// visibilita, archiviati (R10) e ricerca per nome.
export function ReposPage() {
  const { data, loading, error } = useLoad(() => fetchRepos());
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const repos = useMemo(() => data ?? [], [data]);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return repos.filter((r) => {
      if (q && !r.fullName.toLowerCase().includes(q)) return false;
      if (filter === 'archived') return r.archived;
      if (filter === 'all') return true;
      return r.visibility === filter;
    });
  }, [repos, query, filter]);

  return (
    <div>
      <div className="page-h">
        <h1>Repositories</h1>
        {data ? <span className="counter">{repos.length}</span> : null}
        <span className="sp" />
        <Link to="/new" className="btn btn-primary">
          <Plus size={16} aria-hidden="true" />
          New repository
        </Link>
      </div>

      {error ? <ErrorAlert message={error} /> : null}

      <div className="row section-gap">
        <div className="input-icon grow">
          <Search size={16} aria-hidden="true" />
          <input
            className="input"
            type="search"
            aria-label="Find a repository"
            placeholder="Find a repository…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <span className="seg" role="group" aria-label="Filter by visibility">
          {FILTERS.map((f) => (
            <button
              key={f.id}
              type="button"
              className={filter === f.id ? 'active' : undefined}
              aria-pressed={filter === f.id}
              onClick={() => setFilter(f.id)}
            >
              {f.label}
            </button>
          ))}
        </span>
      </div>

      {loading && data === null ? (
        <p className="muted">Loading repositories…</p>
      ) : shown.length === 0 ? (
        error ? null : (
          <EmptyState
            title={repos.length === 0 ? 'No repositories yet' : 'No repositories match'}
            description={repos.length === 0 ? 'Create a repository to start pushing code.' : 'Try another search or filter.'}
            action={
              repos.length === 0 ? (
                <Link to="/new" className="btn btn-primary">
                  New repository
                </Link>
              ) : undefined
            }
          />
        )
      ) : (
        <ul className="list list-clean" aria-label="Repositories">
          {shown.map((r) => (
            <li key={r.id} className={`list-row repo-row${r.archived ? ' archived' : ''}`}>
              <div className="grow">
                <div className="row">
                  <Link to={repoPath(r)} className="repo-link">
                    {r.fullName}
                  </Link>
                  <VisibilityBadge visibility={r.visibility} />
                  {r.archived ? <span className="badge badge-archived">Archived</span> : null}
                </div>
                <p className="muted small">{r.description || (r.empty ? 'Empty repository' : '')}</p>
                <div className="row small muted">
                  <span>
                    {r.archived && r.archivedAt
                      ? `Archived ${new Date(r.archivedAt).toLocaleDateString()}`
                      : `Updated ${new Date(r.updatedAt).toLocaleDateString()}`}
                  </span>
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
