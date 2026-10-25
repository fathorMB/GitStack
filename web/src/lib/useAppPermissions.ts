import { useEffect, useState } from 'react';
import { loadMe } from '../pages/repos/repoAdmin';
import { appPermissions } from './appsPermissions';
import type { AppPermissions } from './appsPermissions';
import { fetchMyRole } from './issuesApi';
import type { Repository } from './reposApi';

/**
 * Permessi Apps dell'utente sul repo (H5). Il ruolo si legge dall'API
 * (fetchMyRole), mai indovinato; finche' non e' noto o se la lettura fallisce
 * nessun pulsante compare. `ready` dice che la risposta e' arrivata.
 */
export function useAppPermissions(repo: Repository): AppPermissions & { ready: boolean } {
  const [state, setState] = useState<{ perms: AppPermissions; ready: boolean }>({ perms: appPermissions(null), ready: false });
  useEffect(() => {
    let live = true;
    void (async () => {
      const [role, me] = await Promise.all([fetchMyRole(repo.id).catch(() => null), loadMe().catch(() => null)]);
      if (live) setState({ perms: appPermissions(role, { archived: repo.archived, instanceAdmin: me?.isAdmin === true }), ready: true });
    })();
    return () => {
      live = false;
    };
  }, [repo.id, repo.archived]);
  return { ...state.perms, ready: state.ready };
}
