import { Info, Folder } from 'lucide-react';
import { useState } from 'react';
import { useOutletContext, useParams } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert, useToast } from '../../components';
import { describeError } from '../../lib/http';
import { fetchDeletedRepos, restoreRepo } from '../../lib/reposApi';
import type { DeletedRepository } from '../../lib/reposApi';
import { daysLeft } from '../../lib/format';
import { useLoad } from '../../lib/useLoad';
import type { SettingsContext } from '../settings/SettingsLayout';

// Repo eliminati dell'owner (mockup 19): per l'utente (/settings/deleted-repos)
// e per l'organizzazione (/orgs/:org/settings/deleted-repos). Il backend
// elenca solo quelli su cui l'utente ha admin (R12).
export function DeletedReposPage() {
  const { org } = useParams<{ org?: string }>();
  // Nelle impostazioni personali l'owner e' l'utente della sessione.
  const ctx = useOutletContext<SettingsContext | undefined>();
  const owner = org ?? ctx?.user.username;
  return <DeletedReposView key={owner ?? ''} owner={owner} />;
}

function DeletedReposView({ owner }: { owner?: string }) {
  const { toast } = useToast();
  const { data, setData, loading, error } = useLoad(() => fetchDeletedRepos(owner));
  const [busyId, setBusyId] = useState('');
  const [actionError, setActionError] = useState('');

  async function restore(item: DeletedRepository) {
    setBusyId(item.id);
    setActionError('');
    try {
      const repo = await restoreRepo(item.id);
      setData((cur) => (cur ?? []).filter((r) => r.id !== item.id));
      toast(`Restored ${repo.fullName}.`, 'success');
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusyId('');
    }
  }

  const items = data ?? [];
  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <div className="row page-h">
        <h1>Deleted repositories</h1>
        {data ? <span className="counter">{items.length}</span> : null}
      </div>
      <p className="muted">
        Repositories deleted in the last 7 days. Restoring brings back code, issues, settings and access exactly as they were.
      </p>
      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {loading && data === null ? <p className="muted">Loading deleted repositories…</p> : null}
      {data !== null && items.length === 0 ? <EmptyState title="No deleted repositories" description="Nothing was deleted in the last 7 days." /> : null}
      {items.length > 0 ? (
        <ul className="list" aria-label="Deleted repositories" style={{ listStyle: 'none', padding: 0 }}>
          {items.map((r) => {
            const days = daysLeft(r.purgeAt);
            return (
              <li key={r.id} className="list-row" style={{ padding: '14px 16px' }}>
                <Folder size={16} className="muted" aria-hidden="true" />
                <div style={{ flex: 1 }}>
                  <div className="mono">
                    {r.owner.name}/{r.name}
                  </div>
                  <div className="small muted">
                    Deleted {new Date(r.deletedAt).toLocaleDateString()} · removed permanently in{' '}
                    <b style={{ color: days <= 1 ? 'var(--danger)' : 'var(--text)' }}>{days === 1 ? '1 day' : `${days} days`}</b>
                  </div>
                </div>
                <Button disabled={busyId === r.id} aria-label={`Restore ${r.owner.name}/${r.name}`} onClick={() => void restore(r)}>
                  Restore
                </Button>
              </li>
            );
          })}
        </ul>
      ) : null}
      <div className="alert alert-info">
        <Info size={16} aria-hidden="true" />
        <div>While a repository is here its name stays reserved and its disk space is not freed.</div>
      </div>
    </div>
  );
}
