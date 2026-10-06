import { Eye } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Select } from '../../components';
import { describeError } from '../../lib/http';
import { fetchRepoWatch, saveRepoWatch } from '../../lib/notificationsApi';
import type { RepoWatchMode } from '../../lib/notificationsApi';

const WATCH_OPTIONS: { value: RepoWatchMode; label: string }[] = [
  { value: 'participating', label: 'Participating' },
  { value: 'all', label: 'All activity' },
  { value: 'ignore', label: 'Ignore' },
];

// Selettore Watch del repo (mockup 07 e 21, C3). Un errore nel caricamento
// nasconde il selettore senza rompere la pagina; uno nel salvataggio ripristina.
export function RepoWatchSelect({ owner, repo, prefix = true }: { owner: string; repo: string; prefix?: boolean }) {
  const [mode, setMode] = useState<RepoWatchMode | null>(null);
  const [failed, setFailed] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    setMode(null);
    setFailed(false);
    fetchRepoWatch(owner, repo).then(
      (w) => live && setMode(w.mode),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [owner, repo]);

  if (failed || mode === null) return null;

  const change = (next: string) => {
    const prev = mode;
    const value = next as RepoWatchMode;
    setMode(value);
    setSaveError(null);
    saveRepoWatch(owner, repo, value).then(
      (w) => setMode(w.mode),
      (err: unknown) => {
        setMode(prev);
        setSaveError(describeError(err));
      },
    );
  };

  return (
    <span className="row" style={{ gap: 6 }}>
      <Eye size={16} aria-hidden="true" />
      {prefix ? <span className="small muted">Watch:</span> : null}
      <Select aria-label={`Watch ${owner}/${repo}`} value={mode} onValueChange={change} options={WATCH_OPTIONS} />
      {saveError ? (
        <span className="small" role="alert" style={{ color: 'var(--danger)' }}>
          {saveError}
        </span>
      ) : null}
    </span>
  );
}
