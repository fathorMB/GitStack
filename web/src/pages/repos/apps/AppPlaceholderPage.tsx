import type { App } from '../../../lib/appsApi';
import type { Repository } from '../../../lib/reposApi';
import { AppHeader } from './AppHeader';
import type { AppMode } from './appMode';

// Segnaposto delle route di Logs (H4, data-screen="logs") e Variables & secrets
// (H6, data-screen="config"): esistono gia' nel router, il contenuto lo
// riempie GIT-362 sostituendo questo componente, senza toccare App.tsx.
export function AppPlaceholderPage({ repo, app, mode }: { repo: Repository; app: App; mode: AppMode }) {
  const title = mode === 'app-logs' ? 'Logs' : 'Variables & secrets';
  return (
    <>
      <AppHeader repo={repo} app={app} mode={mode} />
      <div className="page">
        <p className="muted" data-testid={`placeholder-${mode}`}>
          {title} is not available in this version of the interface yet.
        </p>
      </div>
    </>
  );
}
