import { Info } from 'lucide-react';
import { Link } from 'react-router-dom';
import { purgeDate, trashPath } from './nameInTrash';
import type { NameInTrashInfo } from './nameInTrash';

// Spiega perche' il nome e' occupato, quando si libera e dove si ripristina.
export function NameInTrashNotice({ info, name, currentUser }: { info: NameInTrashInfo; name: string; currentUser: string }) {
  const full = `${info.owner}/${name}`;
  const to = trashPath(info.owner, info.owner === currentUser ? 'user' : 'organization');
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
