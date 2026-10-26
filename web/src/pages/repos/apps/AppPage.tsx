import { useParams } from 'react-router-dom';
import { ErrorAlert } from '../../../components';
import { fetchApp } from '../../../lib/appsApi';
import type { App } from '../../../lib/appsApi';
import type { Repository } from '../../../lib/reposApi';
import { useAppPermissions } from '../../../lib/useAppPermissions';
import { useLoad } from '../../../lib/useLoad';
import { AppAccessPage } from './AppAccessPage';
import { AppConfigPage } from './AppConfigPage';
import { AppLogsPage } from './AppLogsPage';
import { AppOverviewPage } from './AppOverviewPage';
import { AppPlaceholderPage } from './AppPlaceholderPage';
import { AppVersionsPage } from './AppVersionsPage';
import { AppVolumesPage } from './AppVolumesPage';
import type { AppMode } from './appMode';

/** Pagine di un'app: carica l'app una volta e la passa alla scheda scelta dalla route. */
export function AppPage({ repo, mode }: { repo: Repository; mode: AppMode }) {
  const { app: appName = '' } = useParams();
  return <AppLoaded key={`${repo.id}/${appName}`} repo={repo} appName={appName} mode={mode} />;
}

function AppLoaded({ repo, appName, mode }: { repo: Repository; appName: string; mode: AppMode }) {
  const perms = useAppPermissions(repo);
  const { data, setData, loading, error } = useLoad(() => fetchApp(repo.owner.name, repo.name, appName));
  if (loading && data === null) return <p className="muted">Loading app…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'App not found.'} />;
  const onChange = (next: App) => setData(next);
  return (
    <div>
      {mode === 'app-overview' ? (
        <AppOverviewPage repo={repo} app={data} perms={perms} onChange={onChange} />
      ) : mode === 'app-versions' ? (
        <AppVersionsPage repo={repo} app={data} perms={perms} onChange={onChange} />
      ) : mode === 'app-access' ? (
        <AppAccessPage repo={repo} app={data} perms={perms} onChange={onChange} />
      ) : mode === 'app-volumes' ? (
        <AppVolumesPage repo={repo} app={data} perms={perms} onChange={onChange} />
      ) : mode === 'app-logs' ? (
        <AppLogsPage repo={repo} app={data} perms={perms} />
      ) : mode === 'app-config' ? (
        <AppConfigPage repo={repo} app={data} perms={perms} />
      ) : (
        <AppPlaceholderPage repo={repo} app={data} mode={mode} />
      )}
    </div>
  );
}
