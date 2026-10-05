import { Link as LinkIcon, Terminal } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { CopyButton, ErrorAlert } from '../../components';
import { fetchRepo } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { VisibilityBadge } from './ReposPage';
import { isRepoAdmin, loadMe } from './repoAdmin';

// Pagina /<owner>/<repo> (R1): con repo vuoto mostra il quick setup
// (mockup 06); altrimenti un segnaposto in attesa del browser di M-04.
export function RepoPage() {
  const { owner = '', repo = '' } = useParams();
  const { data, loading, error } = useLoad(() => fetchRepo(owner, repo));

  // Link alle impostazioni solo a chi ha admin; un errore nel controllo lo nasconde.
  const [admin, setAdmin] = useState(false);
  useEffect(() => {
    if (!data) return;
    let live = true;
    void (async () => {
      try {
        const ok = await isRepoAdmin(data, await loadMe());
        if (live) setAdmin(ok);
      } catch {
        if (live) setAdmin(false);
      }
    })();
    return () => {
      live = false;
    };
  }, [data]);

  if (loading && data === null) return <p className="muted">Loading repository…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Repository not found.'} />;

  return (
    <div>
      <div className="repo-title row">
        <h1>
          {data.owner.name} / {data.name}
        </h1>
        <VisibilityBadge visibility={data.visibility} />
        {data.archived ? <span className="badge badge-archived">Archived</span> : null}
        <span className="sp" />
        {admin ? <Link to={`/${data.owner.name}/${data.name}/settings`}>Settings</Link> : null}
      </div>
      {data.description ? <p className="muted">{data.description}</p> : null}
      {data.empty ? (
        <QuickSetup repo={data} />
      ) : (
        <p className="muted section-gap">The code browser arrives with M-04.</p>
      )}
    </div>
  );
}

// Gli indirizzi arrivano dalla risposta dell'API (cloneUrls): il client non
// li costruisce mai. L'SSH usa la porta dell'installazione (R7, 2222).
export function QuickSetup({ repo }: { repo: Repository }) {
  const [tab, setTab] = useState<'https' | 'ssh'>('https');
  const url = repo.cloneUrls[tab];
  const branch = repo.defaultBranch || 'main';
  return (
    <section className="card section-gap" aria-label="Quick setup">
      <div className="card-h">
        <Terminal size={16} aria-hidden="true" />
        Quick setup
        <span className="sp" />
        <span className="seg" role="group" aria-label="Clone protocol">
          <button type="button" className={tab === 'https' ? 'active' : undefined} aria-pressed={tab === 'https'} onClick={() => setTab('https')}>
            HTTPS
          </button>
          <button type="button" className={tab === 'ssh' ? 'active' : undefined} aria-pressed={tab === 'ssh'} onClick={() => setTab('ssh')}>
            SSH
          </button>
        </span>
      </div>
      <div className="card-b stack form-stack">
        <div className="row">
          <div className="input-icon grow">
            <LinkIcon size={16} aria-hidden="true" />
            <input className="input mono" aria-label="Clone URL" value={url} readOnly />
          </div>
          <CopyButton value={url} />
        </div>

        <h3>Push an existing repository</h3>
        <div className="cmdblock">
          <span className="p">$ </span>git remote add origin {url}
          {'\n'}
          <span className="p">$ </span>git push -u origin {branch}
        </div>

        <h3>Start from scratch</h3>
        <div className="cmdblock">
          <span className="p">$ </span>echo &quot;# {repo.name}&quot; &gt; README.md{'\n'}
          <span className="p">$ </span>git init &amp;&amp; git add README.md &amp;&amp; git commit -m &quot;first commit&quot;{'\n'}
          <span className="p">$ </span>git branch -M {branch}
          {'\n'}
          <span className="p">$ </span>git remote add origin {url}
          {'\n'}
          <span className="p">$ </span>git push -u origin {branch}
        </div>

        <h3>With the GitStack CLI</h3>
        <div className="cmdblock">
          <span className="p">$ </span>gs repo clone {repo.fullName}
        </div>
      </div>
    </section>
  );
}
