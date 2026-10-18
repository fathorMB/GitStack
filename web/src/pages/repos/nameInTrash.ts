import { ApiError } from '../../lib/http';

// 409 `name_in_trash` di creazione e import (R2): il nome e' tenuto da un repo
// eliminato che resta 7 giorni nel cestino. `purgeAt` e `owner` arrivano dal
// server; `repoId` solo se il chiamante e' admin di quel repo.
export interface NameInTrashInfo {
  purgeAt: string;
  owner: string;
  repoId?: string;
}

export function nameInTrashInfo(err: unknown): NameInTrashInfo | null {
  if (!(err instanceof ApiError) || err.code !== 'name_in_trash') return null;
  const d = err.details ?? {};
  if (typeof d.purgeAt !== 'string') return null;
  return {
    purgeAt: d.purgeAt,
    owner: typeof d.owner === 'string' ? d.owner : '',
    ...(typeof d.repoId === 'string' ? { repoId: d.repoId } : {}),
  };
}

/** Pagina del cestino per l'owner: personale per l'utente, altrimenti dell'organizzazione. */
export function trashPath(ownerName: string, ownerType: 'user' | 'organization'): string {
  return ownerType === 'organization' ? `/orgs/${ownerName}/deleted-repos` : '/settings/deleted-repos';
}

/** Data (dal server) della cancellazione definitiva, nel formato locale dell'utente. */
export function purgeDate(purgeAt: string): string {
  return new Date(purgeAt).toLocaleDateString();
}
