import { AlertTriangle, Box, Info, Lock, ShieldCheck, Tag as TagIcon, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, ConfirmDialog, CopyButton, EmptyState, ErrorAlert, useToast } from '../../components';
import { formatBytes, timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import { deletePackage, fetchPackages } from '../../lib/packagesApi';
import type { Package } from '../../lib/packagesApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

/** Cosa si sta per eliminare: un'immagine intera o un solo tag. */
interface Target {
  pkg: Package;
  tag?: string;
}

/** Riga di tag: dal contratto (tagDetails); senza, solo il nome, nessun dato inventato. */
interface TagRow {
  name: string;
  digest?: string;
  sizeBytes?: number;
  pushedAt?: string;
  signature?: 'verified' | 'unverified';
  hasSbom?: boolean;
  protected?: boolean;
  removed?: boolean;
  rule?: string;
  removedAt?: string;
}

function tagRows(pkg: Package): TagRow[] {
  const current = pkg.tagDetails ?? pkg.tags.map((name) => ({ name }));
  return [...current, ...(pkg.removedTags ?? []).map((tag) => ({ name: tag.name, digest: tag.digest, removed: true, rule: tag.rule, removedAt: tag.removedAt }))];
}

/** Tag più recente (ultimo push); senza date, il primo. */
function latestTag(pkg: Package): string | undefined {
  let best: TagRow | undefined;
  for (const r of tagRows(pkg)) {
    if (!best || (r.pushedAt && (!best.pushedAt || r.pushedAt > best.pushedAt))) best = r;
  }
  return best?.name;
}

/** Scheda Packages (RG1, mockup B5): elenco immagini, tag dell'immagine scelta, comando di pull. L'eliminazione è per chi ha admin. */
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
  const [selected, setSelected] = useState('');

  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading packages…</p>;

  const current = data.items.find((p) => p.name === selected) ?? data.items[0];

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
    <div className="stack">
      {deleteError ? <ErrorAlert message={deleteError} /> : null}
      {!current ? (
        <EmptyState
          icon={<Box size={32} aria-hidden="true" />}
          title="No packages yet"
          description={`Push an image to ${window.location.host}/${owner}/${name} with docker, or publish it from a CI job.`}
        />
      ) : (
        <div className="two" style={{ gridTemplateColumns: '1fr 340px' }}>
          <div className="stack" style={{ gap: 16 }}>
            <div className="list" role="list" aria-label="Images">
              <div className="list-row list-head">
                <b style={{ color: 'var(--text)' }}>Images</b>
                <span className="sp" />
                <span className="small">Visibility follows the repository: {repo.visibility}</span>
              </div>
              {data.items.map((p) => (
                <ImageRow key={p.name} pkg={p} active={p.name === current.name} onSelect={() => setSelected(p.name)} />
              ))}
            </div>
            <TagsCard pkg={current} repo={repo} admin={admin} onDelete={(tag) => setTarget({ pkg: current, tag })} />
          </div>
          <div className="stack" style={{ gap: 16 }}>
            <PullCard pkg={current} />
            <div className="alert alert-info">
              <ShieldCheck size={16} aria-hidden="true" />
              <div>
                The CI <b>signs every image it publishes</b> with this instance&apos;s key. Images pushed by hand have no signature and show <i>not verified</i>.
              </div>
            </div>
            <div className="alert alert-info">
              <Info size={16} aria-hidden="true" />
              <div>
                Images are <b>not in backups</b> by default: they can be rebuilt by CI from tags. Images used by apps (active and previous version) are <b>always</b> included. An admin can include all
                images in Settings · Packages.
              </div>
            </div>
          </div>
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

function ImageRow({ pkg, active, onSelect }: { pkg: Package; active: boolean; onSelect: () => void }) {
  const latest = latestTag(pkg);
  const helm = pkg.kind === 'helm';
  const count = pkg.tags.length;
  return (
    <div className={active ? 'list-row selected' : 'list-row hover'} role="listitem" aria-label={`Package ${pkg.name}`}>
      <button type="button" className="row-main" aria-pressed={active} aria-label={`Select ${pkg.name}`} onClick={onSelect}>
        <Box size={16} className="muted" aria-hidden="true" />
        <div style={{ flex: 1, textAlign: 'left' }}>
          <span className="mono" style={{ fontWeight: 600 }}>
            {pkg.name}
          </span>{' '}
          {helm ? <span className="badge">Helm</span> : null}
          <div className="small muted">
            {latest ? (
              <>
                Latest <span className="mono">{latest}</span> ·{' '}
              </>
            ) : null}
            {count} {helm ? (count === 1 ? 'version' : 'versions') : count === 1 ? 'tag' : 'tags'}
            {helm ? '' : ` · ${formatBytes(pkg.sizeBytes)}`}
          </div>
        </div>
        <span className="small muted" title={pkg.updatedAt}>
          {pkg.pushedBy ? (
            <>
              pushed {timeAgo(pkg.updatedAt)} by <b>{pkg.pushedBy.login}</b>
            </>
          ) : (
            timeAgo(pkg.updatedAt)
          )}
        </span>
      </button>
    </div>
  );
}

function TagsCard({ pkg, repo, admin, onDelete }: { pkg: Package; repo: Repository; admin: boolean; onDelete: (tag?: string) => void }) {
  const rows = tagRows(pkg);
  return (
    <div className="card" role="region" aria-label={`Tags of ${pkg.name}`}>
      <div className="card-h">
        <span className="mono">{pkg.name}</span>
        <span className="sp" />
        {admin ? (
          <>
            <Link className="btn btn-sm" to={`/${repo.owner.name}/${repo.name}/settings/packages`}>
              Cleanup rules
            </Link>
            <Button size="sm" variant="ghost" aria-label={`Delete ${pkg.name}`} onClick={() => onDelete()}>
              <Trash2 size={14} aria-hidden="true" />
            </Button>
          </>
        ) : null}
      </div>
      <div className="list" style={{ border: 0, borderRadius: 0 }} role="list" aria-label={`Tag list of ${pkg.name}`}>
        {rows.length === 0 ? (
          <div className="list-row small muted">Untagged image: removed by cleanup after 7 days unless it is tagged.</div>
        ) : (
          rows.map((t) => (
            <div key={t.name} className="list-row" role="listitem" aria-label={`Tag ${t.name}`}>
              <TagIcon size={16} aria-hidden="true" />
              <span className="mono" style={{ width: 160 }}>
                {t.name}
              </span>
              {t.removed ? <span className="small muted">removed {t.rule} by rule</span> : null}
              {t.protected ? (
                <span className="badge">
                  <Lock size={12} aria-hidden="true" />
                  protected
                </span>
              ) : null}
              {t.signature === 'verified' ? (
                <span className="badge badge-ok">
                  <ShieldCheck size={12} aria-hidden="true" />
                  signed by CI
                </span>
              ) : null}
              {t.signature === 'unverified' ? (
                <span className="badge badge-run">
                  <AlertTriangle size={12} aria-hidden="true" />
                  not verified
                </span>
              ) : null}
              {t.hasSbom ? <span className="badge">SBOM</span> : null}
              <span className="sp" />
              {t.signature === 'unverified' ? <span className="small muted">pushed by hand with docker push · no signature</span> : null}
              {t.digest ? (
                <span className="small muted" title={t.digest}>
                  {t.digest.slice(0, 11)}…{t.digest.slice(-2)}
                  {t.sizeBytes !== undefined ? ` · ${formatBytes(t.sizeBytes)}` : ''}
                </span>
              ) : null}
              {admin ? (
                <Button variant="ghost" size="sm" aria-label={`Delete tag ${t.name} of ${pkg.name}`} onClick={() => onDelete(t.name)}>
                  <Trash2 size={12} aria-hidden="true" />
                </Button>
              ) : null}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function PullCard({ pkg }: { pkg: Package }) {
  const host = window.location.host;
  const latest = latestTag(pkg);
  const login = `docker login ${host}`;
  const pull = latest ? `docker pull ${host}/${pkg.name}:${latest}` : pkg.digest ? `docker pull ${host}/${pkg.name}@${pkg.digest}` : `docker pull ${host}/${pkg.name}`;
  return (
    <div className="card card-b stack" style={{ gap: 10 }}>
      <b>Pull</b>
      <div className="row">
        <div className="cmdblock grow" style={{ marginTop: 0, whiteSpace: 'pre-wrap' }} aria-label={`Pull command for ${pkg.name}`}>
          <span className="p">$ </span>
          {login}
          {'\n'}
          <span className="p">$ </span>
          {pull}
        </div>
        <CopyButton value={`${login}\n${pull}`} />
      </div>
      <p className="hint">
        Log in with a personal or agent token with the <span className="mono">registry</span> scope. Inside workflows the job token works.
      </p>
    </div>
  );
}
