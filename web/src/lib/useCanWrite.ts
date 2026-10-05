import { useEffect, useState } from 'react';
import { loadMe } from '../pages/repos/repoAdmin';
import { fetchMyRole } from './issuesApi';
import type { Repository } from './reposApi';

/**
 * Etichette e milestone le gestisce chi ha `write` (I5, I7). Il ruolo si legge
 * dall'API, mai indovinato; un repo archiviato e' in sola lettura (R10).
 */
export function useCanWrite(repo: Repository): boolean {
  const [write, setWrite] = useState(false);
  useEffect(() => {
    let live = true;
    void (async () => {
      const [role, me] = await Promise.all([fetchMyRole(repo.id).catch(() => null), loadMe().catch(() => null)]);
      const ok = role === 'write' || role === 'admin' || me?.isAdmin === true;
      if (live) setWrite(ok && !repo.archived);
    })();
    return () => {
      live = false;
    };
  }, [repo.id, repo.archived]);
  return write;
}
