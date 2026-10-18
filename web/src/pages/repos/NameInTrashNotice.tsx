import { Info } from 'lucide-react';
import { Link } from 'react-router-dom';
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

// Spiega perche' il nome e' occupato, quando si libera e dove si ripristina.
export function NameInTrashNotice({ info, name, currentUser }: { info: NameInTrashInfo; name: string; currentUser: string }) {
  const full = `${info.owner}/${name}`;
  const personal = info.owner === currentUser;
  const to = trashPath(info.owner, personal ? 'user' : 'organization');
  return (
    <div className="alert alert-danger" role="alert">
      <Info size={16} aria-hidden="true" />
      <div>
        <b>The name {full} is held by a deleted repository.</b> Deleted repositories stay in the trash for 7 days and keep their name:
        it becomes free on <b>{purgeDate(info.purgeAt)}</b>, when the repository is removed permanently.{' '}
        {info.repoId ? (
          <>
            You are an admin of it: open <Link to={to}>Deleted repositories</Link> and restore it to get it back now.
          </>
        ) : (
          <>An admin of that repository can restore it from Deleted repositories before then.</>
        )}
      </div>
    </div>
  );
}
