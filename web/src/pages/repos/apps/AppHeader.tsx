import { Globe, LayoutGrid, Lock } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import type { ReactNode } from 'react';
import type { App } from '../../../lib/appsApi';
import { accessLabel, statusBadge } from '../../../lib/appsView';
import type { Repository } from '../../../lib/reposApi';
import { appsPath } from './appMode';
import type { AppMode } from './appMode';

/** Intestazione dell'app (mockup «app-h»): titolo, stato, accesso e, nella Overview, le azioni. */
export function AppHeader({ repo, app, mode, actions }: { repo: Repository; app: App; mode: AppMode; actions?: ReactNode }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const badge = statusBadge(app.status, app.runningPreviousVersion);
  const tab = ({ isActive }: { isActive: boolean }) => (isActive ? 'tab active' : 'tab');
  const settings = mode === 'app-config' || mode === 'app-access' || mode === 'app-volumes';
  return (
    <div className="repo-h">
      <div className="inner">
        <div className="app-h row">
          <LayoutGrid size={22} className="muted" aria-hidden="true" />
          <h1>
            <NavLink className="muted" style={{ fontWeight: 400 }} to={appsPath(owner, name)}>
              {owner} / {name} /
            </NavLink>{' '}
            {app.name}
          </h1>
          <span className={badge.tone === 'none' ? 'badge' : `badge badge-${badge.tone}`}>{badge.label}</span>
          <span className={app.access === 'public' ? 'badge badge-accent' : 'badge'}>
            {app.access === 'public' ? <Globe size={12} aria-hidden="true" /> : <Lock size={12} aria-hidden="true" />}
            {accessLabel(app.access)}
          </span>
          <span className="sp" />
          {actions}
        </div>
        {actions && app.address ? (
          <div className="small muted" style={{ marginBottom: 6 }}>
            <span className="mono">https://{app.address}</span>
          </div>
        ) : null}
        <nav className="tabs" aria-label="App sections">
          <NavLink className={tab} to={appsPath(owner, name, app.name)} end>
            Overview
          </NavLink>
          <NavLink className={tab} to={appsPath(owner, name, app.name, 'logs')}>
            Logs
          </NavLink>
          <NavLink className={tab} to={appsPath(owner, name, app.name, 'versions')}>
            Versions
          </NavLink>
          <NavLink className={settings ? 'tab active' : 'tab'} to={appsPath(owner, name, app.name, 'settings')}>
            Settings
          </NavLink>
        </nav>
      </div>
    </div>
  );
}
