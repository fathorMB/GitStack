import { Box, CircleDot, FileCode, GitPullRequest, Play, Settings, Tag } from 'lucide-react';
import { useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { PullMirrorBadge } from '../../components';
import { countIssues } from '../../lib/issuesApi';
import { countPulls } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { RepoWatchSelect } from './RepoWatchSelect';
import { VisibilityBadge } from './ReposPage';

// Sezione corrente del repo: decide quale scheda e' attiva.
export type RepoHeaderMode =
  | 'tree'
  | 'commits'
  | 'commit'
  | 'tags'
  | 'releases'
  | 'search'
  | 'blob'
  | 'blame'
  | 'issues'
  | 'issue'
  | 'newissue'
  | 'labels'
  | 'milestones'
  | 'pulls'
  | 'pull'
  | 'newpull'
  | 'pullfiles'
  | 'actions'
  | 'run'
  | 'packages'
  | 'settings'
  | 'webhooks';

// Regola dell'attiva per elenco esplicito: un mode nuovo non accende nessuna scheda.
const CODE_MODES: readonly RepoHeaderMode[] = ['tree', 'commits', 'commit', 'search', 'blob', 'blame'];
const RELEASES_MODES: readonly RepoHeaderMode[] = ['releases', 'tags'];
const ISSUES_MODES: readonly RepoHeaderMode[] = ['issues', 'issue', 'newissue', 'labels', 'milestones'];
const PULLS_MODES: readonly RepoHeaderMode[] = ['pulls', 'pull', 'pullfiles', 'newpull'];
const ACTIONS_MODES: readonly RepoHeaderMode[] = ['actions', 'run'];
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

  // Idem per le Pull Request aperte.
  const [openPulls, setOpenPulls] = useState<number | null>(null);
  useEffect(() => {
    let live = true;
    countPulls(owner, name, 'is:open').then(
      (n) => {
        if (live) setOpenPulls(n);
      },
      () => {
        if (live) setOpenPulls(null);
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
        <NavLink className={tab(PULLS_MODES.includes(mode))} to={`${base}/pulls`}>
          <GitPullRequest size={16} aria-hidden="true" />
          Pull requests {openPulls !== null ? <span className="counter">{openPulls}</span> : null}
        </NavLink>
        <NavLink className={tab(ACTIONS_MODES.includes(mode))} to={`${base}/actions`}>
          <Play size={16} aria-hidden="true" />
          Actions
        </NavLink>
        <NavLink className={tab(RELEASES_MODES.includes(mode))} to={`${base}/releases`}>
          <Tag size={16} aria-hidden="true" />
          Releases
        </NavLink>
        <NavLink className={tab(mode === 'packages')} to={`${base}/packages`}>
          <Box size={16} aria-hidden="true" />
          Packages
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
