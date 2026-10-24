import { Box, FileCheck2, ShieldAlert, ShieldCheck, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, ConfirmDialog, CopyButton, EmptyState, ErrorAlert, useToast } from '../../components';
import { formatBytes, timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import { deletePackage, fetchPackages, pullCommand } from '../../lib/packagesApi';
import type { Package } from '../../lib/packagesApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

/** Cosa si sta per eliminare: un'immagine intera o un solo tag. */
interface Target {
  pkg: Package;
  tag?: string;
}

/** Scheda Packages (RG1, mockup B5): immagini del repo con tag, firma, SBOM e comando di pull. L'eliminazione è per chi ha admin. */
export function PackagesPage({ repo, admin }: { repo: Repository; admin: boolean }) {
  const [version, setVersion] = useState(0);
  return <PackagesLoaded key={version} repo={repo} admin={admin} reload={() => setVersion((v) => v + 1)} />;
}

function PackagesLoaded({ repo, admin, reload }: { repo: Repository; admin: boolean; reload: () => void }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { toast } = useToast();
  const { data, error } = useLoad(() => fetchPackages(owner, name));
  const [target, setTarget] = useState<Target | null>(null);
  const [deleteError, setDeleteError] = useState('');

  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading packages…</p>;

  async function confirmDelete() {
    if (!target) return;
    const { pkg, tag } = target;
    setTarget(null);
    setDeleteError('');
    try {
      await deletePackage(owner, name, pkg.name, tag);
      toast(tag ? `Tag ${tag} deleted.` : `${pkg.name} deleted.`, 'success');
      reload();
    } catch (err) {
      setDeleteError(describeError(err));
    }
  }

  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <div className="page-h row">
        <h2>Packages</h2>
        <span className="counter">{data.items.length}</span>
        <span className="sp" />
        {admin ? (
          <Link className="small" to={`/${owner}/${name}/settings/packages`}>
            Package settings
          </Link>
        ) : null}
      </div>
      {deleteError ? <ErrorAlert message={deleteError} /> : null}
      {data.items.length === 0 ? (
        <EmptyState
          icon={<Box size={32} aria-hidden="true" />}
          title="No packages yet"
          description={`Push an image to ${window.location.host}/${owner}/${name} with docker, or publish it from a CI job.`}
        />
      ) : (
        <div className="stack" role="list" aria-label="Packages">
          {data.items.map((p) => (
            <PackageCard key={p.name} pkg={p} admin={admin} onDelete={(tag) => setTarget({ pkg: p, tag })} />
          ))}
        </div>
      )}
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(o) => {
          if (!o) setTarget(null);
        }}
        title={target?.tag ? 'Delete tag' : 'Delete image'}
        description={
          target?.tag ? (
            <>
              Delete the tag <span className="mono">{target.tag}</span> of <span className="mono">{target.pkg.name}</span>? Protected tags can&apos;t be deleted.
            </>
          ) : (
            <>
              Delete <span className="mono">{target?.pkg.name}</span> with all its tags? This can&apos;t be undone.
            </>
          )
        }
        confirmLabel="Delete"
        destructive
        confirmInput={target && !target.tag ? { placeholder: target.pkg.name, expected: target.pkg.name } : undefined}
        onConfirm={() => void confirmDelete()}
      />
    </div>
  );
}

function PackageCard({ pkg, admin, onDelete }: { pkg: Package; admin: boolean; onDelete: (tag?: string) => void }) {
  const host = window.location.host;
  const firstTag = pkg.tags[0];
  const command = pullCommand(host, pkg.name, firstTag, pkg.digest);
  const verified = pkg.signature === 'verified';
  return (
    <article className="card" role="listitem" aria-label={`Package ${pkg.name}`}>
      <div className="card-h">
        <Box size={16} aria-hidden="true" />
        <span className="mono">{pkg.name}</span>
        <span className="sp" />
        {verified ? (
          <span className="badge badge-ok">
            <ShieldCheck size={12} aria-hidden="true" /> Signature verified
          </span>
        ) : (
          <span className="badge badge-archived">
            <ShieldAlert size={12} aria-hidden="true" /> Signature not verified
          </span>
        )}
        {pkg.hasSbom ? (
          <span className="badge badge-accent">
            <FileCheck2 size={12} aria-hidden="true" /> SBOM
          </span>
        ) : (
          <span className="badge">No SBOM</span>
        )}
        {admin ? (
          <Button size="sm" variant="ghost" aria-label={`Delete ${pkg.name}`} onClick={() => onDelete()}>
            <Trash2 size={14} aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      <div className="card-b stack">
        <div className="row small muted" style={{ gap: 12 }}>
          <span>{formatBytes(pkg.sizeBytes)}</span>
          <span title={pkg.updatedAt}>Updated {timeAgo(pkg.updatedAt)}</span>
          {pkg.digest ? <span className="mono">{pkg.digest.slice(0, 19)}</span> : null}
        </div>
        {pkg.tags.length === 0 ? (
          <p className="muted small">Untagged image: removed by cleanup unless it is tagged.</p>
        ) : (
          <ul className="row" aria-label={`Tags of ${pkg.name}`} style={{ gap: 6, flexWrap: 'wrap', listStyle: 'none', padding: 0, margin: 0 }}>
            {pkg.tags.map((t) => (
              <li key={t}>
                <span className="badge mono">
                  {t}
                  {admin ? (
                    <Button variant="ghost" size="sm" aria-label={`Delete tag ${t} of ${pkg.name}`} onClick={() => onDelete(t)}>
                      <Trash2 size={12} aria-hidden="true" />
                    </Button>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        )}
        <div className="row">
          <div className="cmdblock grow" style={{ marginTop: 0 }} aria-label={`Pull command for ${pkg.name}`}>
            <span className="p">$ </span>
            {command}
          </div>
          <CopyButton value={command} />
        </div>
      </div>
    </article>
  );
}
