import { CircleDot, FileCode, Settings } from 'lucide-react';
import { useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { PullMirrorBadge } from '../../components';
import { countIssues } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { RepoWatchSelect } from './RepoWatchSelect';
import { VisibilityBadge } from './ReposPage';

// Sezione corrente del repo: decide quale scheda e' attiva.
export type RepoHeaderMode =
  | 'tree'
  | 'commits'
  | 'commit'
  | 'tags'
  | 'search'
  | 'blob'
  | 'blame'
  | 'issues'
  | 'issue'
  | 'newissue'
  | 'labels'
  | 'milestones'
  | 'settings'
  | 'webhooks';

// Regola dell'attiva per elenco esplicito: un mode nuovo non accende nessuna scheda.
const CODE_MODES: readonly RepoHeaderMode[] = ['tree', 'commits', 'commit', 'tags', 'search', 'blob', 'blame'];
const ISSUES_MODES: readonly RepoHeaderMode[] = ['issues', 'issue', 'newissue', 'labels', 'milestones'];
const SETTINGS_MODES: readonly RepoHeaderMode[] = ['settings', 'webhooks'];

// Intestazione del repo condivisa (titolo, badge, Watch, descrizione, schede):
// riceve il repo gia' caricato dalla pagina, non lo scarica di nuovo.
// La scheda Settings compare solo se `admin` (regola di repoAdmin.ts).
export function RepoHeader({ repo, mode, admin }: { repo: Repository; mode: RepoHeaderMode; admin: boolean }) {
  const { owner, name } = { owner: repo.owner.name, name: repo.name };

  // Conteggio delle issues aperte per la scheda; un errore lo nasconde.
  const [openIssues, setOpenIssues] = useState<number | null>(null);
  useEffect(() => {
    let live = true;
    countIssues(owner, name, 'is:open').then(
      (n) => {
        if (live) setOpenIssues(n);
      },
      () => {
        if (live) setOpenIssues(null);
      },
    );
    return () => {
      live = false;
    };
  }, [owner, name]);

  const base = `/${owner}/${name}`;
  const tab = (active: boolean) => () => (active ? 'tab active' : 'tab');

  return (
    <>
      <div className="repo-title row">
        <h1>
          {owner} / {name}
        </h1>
        <VisibilityBadge visibility={repo.visibility} />
        {repo.mirror ? <PullMirrorBadge mirror={repo.mirror} /> : null}
        {repo.archived ? <span className="badge badge-archived">Archived</span> : null}
        <span className="sp" />
        <RepoWatchSelect owner={owner} repo={name} />
      </div>
      {repo.description ? <p className="muted">{repo.description}</p> : null}
      <nav className="tabs" aria-label="Repository sections">
        <NavLink className={tab(CODE_MODES.includes(mode))} to={base} end>
          <FileCode size={16} aria-hidden="true" />
          Code
        </NavLink>
        <NavLink className={tab(ISSUES_MODES.includes(mode))} to={`${base}/issues`}>
          <CircleDot size={16} aria-hidden="true" />
          Issues {openIssues !== null ? <span className="counter">{openIssues}</span> : null}
        </NavLink>
        {admin ? (
          <NavLink className={tab(SETTINGS_MODES.includes(mode))} to={`${base}/settings`}>
            <Settings size={16} aria-hidden="true" />
            Settings
          </NavLink>
        ) : null}
      </nav>
    </>
  );
}
