import { Download, Info, Search, Tag as TagIcon } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { EmptyState, ErrorAlert } from '../../components';
import { archiveUrl, commitHref, fetchTags } from '../../lib/codeApi';
import type { Tag } from '../../lib/codeApi';
import { timeAgo } from '../../lib/format';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

/** Pagina Tags (mockup 23, B7): data, commit, messaggio dei tag annotati, ZIP e tar.gz (B3). */
export function TagsPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data, error } = useLoad(() => fetchTags(owner, name));
  const [q, setQ] = useState('');
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading tags…</p>;
  const sorted = [...data.items].sort((a, b) => Date.parse(b.taggedAt) - Date.parse(a.taggedAt));
  const latest = sorted[0]?.name;
  const shown = sorted.filter((t) => t.name.toLowerCase().includes(q.trim().toLowerCase()));
  const example = sorted[0]?.name ?? 'v1.0.0';
  const curl = `curl -H "Authorization: Bearer $TOKEN" -L -o ${name}-${example}.tar.gz '${window.location.origin}${archiveUrl(owner, name, example, 'tar.gz')}'`;
  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <div className="page-h row">
        <h2>Tags</h2>
        <span className="counter">{data.total}</span>
        <span className="sp" />
        <div className="input-icon" style={{ width: 240 }}>
          <Search size={16} aria-hidden="true" />
          <input className="input" aria-label="Find a tag" placeholder="Find a tag…" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
      </div>
      {data.items.length === 0 ? <EmptyState icon={<TagIcon size={32} aria-hidden="true" />} title="No tags yet" description="Create a tag with git tag and publish it with git push origin --tags." /> : null}
      {data.items.length > 0 && shown.length === 0 ? <p className="muted">No tags match your search.</p> : null}
      {shown.length > 0 ? (
        <ul className="list" aria-label="Tags" style={{ listStyle: 'none', padding: 0 }}>
          {shown.map((t) => (
            <TagRow key={t.name} owner={owner} repo={name} tag={t} latest={t.name === latest} />
          ))}
        </ul>
      ) : null}
      <div className="alert alert-info" role="note">
        <Info size={16} aria-hidden="true" />
        <div>
          Downloads need you to be signed in, or an access token with read access:{' '}
          <span className="mono">
            {curl}
          </span>
        </div>
      </div>
    </div>
  );
}

function TagRow({ owner, repo, tag, latest }: { owner: string; repo: string; tag: Tag; latest: boolean }) {
  return (
    <li className="list-row" style={{ padding: '14px 16px', alignItems: 'flex-start' }}>
      <TagIcon size={16} aria-hidden="true" style={{ marginTop: 3, color: latest ? 'var(--success)' : undefined }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="row">
          <b className="mono">{tag.name}</b>
          {latest ? <span className="badge badge-accent">Latest</span> : null}
        </div>
        {tag.annotated && tag.message ? (
          <p className="small muted" style={{ marginTop: 4, whiteSpace: 'pre-wrap' }}>
            {tag.message}
          </p>
        ) : (
          <p className="small subtle" style={{ marginTop: 4 }}>
            Lightweight tag (no message)
          </p>
        )}
        <div className="row small muted" style={{ marginTop: 6, gap: 14 }}>
          <span title={tag.taggedAt}>{timeAgo(tag.taggedAt)}</span>
          <Link className="mono" to={commitHref(owner, repo, tag.commit.sha)}>
            {tag.commit.sha.slice(0, 7)}
          </Link>
        </div>
      </div>
      <div className="row" style={{ gap: 6 }}>
        <a className="btn btn-sm" href={archiveUrl(owner, repo, tag.name, 'zip')} aria-label={`Download ${tag.name} as ZIP`}>
          <Download size={14} aria-hidden="true" />
          ZIP
        </a>
        <a className="btn btn-sm" href={archiveUrl(owner, repo, tag.name, 'tar.gz')} aria-label={`Download ${tag.name} as tar.gz`}>
          <Download size={14} aria-hidden="true" />
          tar.gz
        </a>
      </div>
    </li>
  );
}
