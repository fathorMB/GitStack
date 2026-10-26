// Schermate della scheda Apps. Le route stanno tutte in App.tsx, comprese
// Logs (H4) e Variables & secrets (H6) che riempie GIT-362: il router non si tocca piu'.
export type AppMode = 'apps' | 'appnew' | 'app-overview' | 'app-logs' | 'app-versions' | 'app-config' | 'app-access' | 'app-volumes';

export const APP_MODES: readonly string[] = ['apps', 'appnew', 'app-overview', 'app-logs', 'app-versions', 'app-config', 'app-access', 'app-volumes'];

export function isAppMode(mode: string): mode is AppMode {
  return APP_MODES.includes(mode);
}

/** Indirizzi della scheda: un solo posto, usato da header, elenco e test. */
export function appsPath(owner: string, repo: string, app?: string, sub?: 'logs' | 'versions' | 'settings' | 'settings/access' | 'settings/volumes'): string {
  const base = `/${owner}/${repo}/apps`;
  if (!app) return base;
  return sub ? `${base}/${app}/${sub}` : `${base}/${app}`;
}
