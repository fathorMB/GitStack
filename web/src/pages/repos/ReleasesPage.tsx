import { Download, Plus, Tag as TagIcon } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button, Checkbox, EmptyState, ErrorAlert, FormField, Markdown, TextInput } from '../../components';
import { archiveUrl } from '../../lib/codeApi';
import { formatBytes, timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import { createRelease, fetchReleases, releaseAssetUrl, shortSha, updateRelease, uploadReleaseAsset } from '../../lib/releasesApi';
import type { Release } from '../../lib/releasesApi';
import type { Repository } from '../../lib/reposApi';
import { useCanWrite } from '../../lib/useCanWrite';
import { useLoad } from '../../lib/useLoad';

/** Segmento Releases | Tags del mockup B7: Tags resta raggiungibile da qui. */
export function ReleasesTagsSwitch({ owner, repo, active }: { owner: string; repo: string; active: 'releases' | 'tags' }) {
  return (
    <span className="seg" role="group" aria-label="Releases or tags">
      <Link className={active === 'releases' ? 'active' : undefined} aria-current={active === 'releases' ? 'page' : undefined} to={`/${owner}/${repo}/releases`}>
        Releases
      </Link>
      <Link className={active === 'tags' ? 'active' : undefined} aria-current={active === 'tags' ? 'page' : undefined} to={`/${owner}/${repo}/tags`}>
        Tags
      </Link>
    </span>
  );
}

/** Pagina Releases (REL1, mockup B7): Latest, pre-release, bozza (solo per chi ha write), note e file allegati con SHA-256. */
export function ReleasesPage({ repo }: { repo: Repository }) {
  const [version, setVersion] = useState(0);
  return <ReleasesLoaded key={version} repo={repo} reload={() => setVersion((v) => v + 1)} />;
}

function ReleasesLoaded({ repo, reload }: { repo: Repository; reload: () => void }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const canWrite = useCanWrite(repo);
  const { data, error } = useLoad(() => fetchReleases(owner, name));
  const [editing, setEditing] = useState<Release | 'new' | null>(null);
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading releases…</p>;
  // Le bozze le vede solo chi ha write (il server le filtra, ma non ci si affida).
  const items = data.items.filter((r) => canWrite || !r.draft);
  const done = () => {
    setEditing(null);
    reload();
  };
  return (
    <div className="stack" style={{ maxWidth: 980 }}>
      <div className="page-h row">
        <ReleasesTagsSwitch owner={owner} repo={name} active="releases" />
        <span className="counter">{items.length}</span>
        <span className="sp" />
        {canWrite && editing === null ? (
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus size={16} aria-hidden="true" />
            Draft a new release
          </Button>
        ) : null}
      </div>
      {editing !== null ? <ReleaseForm owner={owner} repo={name} release={editing === 'new' ? null : editing} onDone={done} onCancel={() => setEditing(null)} /> : null}
      {items.length === 0 && editing === null ? (
        <EmptyState
          icon={<TagIcon size={32} aria-hidden="true" />}
          title="No releases yet"
          description="Releases bundle the notes and files of a version. Draft one from a tag."
        />
      ) : null}
      {items.length > 0 ? (
        <div className="card card-b" role="list" aria-label="Releases">
          {items.map((r) => (
            <ReleaseItem key={r.id} owner={owner} repo={name} release={r} canWrite={canWrite} onEdit={() => setEditing(r)} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ReleaseItem({ owner, repo, release: r, canWrite, onEdit }: { owner: string; repo: string; release: Release; canWrite: boolean; onEdit: () => void }) {
  const when = r.publishedAt ?? r.createdAt;
  return (
    <article className="rel" role="listitem" aria-label={`Release ${r.name}`}>
      <div className="stack small muted" style={{ gap: 6 }}>
        <span className="mono" style={{ color: 'var(--text)' }}>
          <TagIcon size={14} aria-hidden="true" /> {r.tagName}
        </span>
        {r.draft ? <span>not tagged yet</span> : <span title={when}>{timeAgo(when)}</span>}
      </div>
      <div className="md">
        <div className="row">
          <h2 style={{ fontSize: 20 }}>{r.name}</h2>
          {r.latest ? <span className="badge badge-latest">Latest</span> : null}
          {r.prerelease ? <span className="badge badge-accent">Pre-release</span> : null}
          {r.draft ? <span className="badge">Draft · visible to writers only</span> : null}
          <span className="sp" />
          {canWrite ? (
            <Button size="sm" onClick={onEdit} aria-label={`Edit ${r.name}`}>
              Edit
            </Button>
          ) : null}
        </div>
        {r.body.trim() ? <Markdown source={r.body} repo={{ owner, name: repo }} /> : null}
        <h3>
          Assets <span className="counter">{r.assets.length}</span>
        </h3>
        <ul className="list" aria-label={`Assets of ${r.name}`} style={{ marginTop: 6, listStyle: 'none', padding: 0 }}>
          {r.assets.map((a) => (
            <li key={a.id} className="list-row small">
              <Download size={14} aria-hidden="true" />
              <a className="mono" style={{ flex: 1 }} href={releaseAssetUrl(owner, repo, r.id, a.id)}>
                {a.name}
              </a>
              <span className="muted">{formatBytes(a.sizeBytes)}</span>
              <span className="mono subtle" title={a.sha256}>
                {shortSha(a.sha256)}
              </span>
            </li>
          ))}
          {!r.draft ? (
            <li className="list-row small">
              <Download size={14} aria-hidden="true" />
              <span style={{ flex: 1 }}>Source code</span>
              <a href={archiveUrl(owner, repo, r.tagName, 'zip')} aria-label={`Download ${r.tagName} as ZIP`}>
                zip
              </a>
              <a href={archiveUrl(owner, repo, r.tagName, 'tar.gz')} aria-label={`Download ${r.tagName} as tar.gz`}>
                tar.gz
              </a>
            </li>
          ) : null}
        </ul>
      </div>
    </article>
  );
}

/** Creazione (release null) o modifica; i file si caricano dopo il salvataggio, uno per uno. */
function ReleaseForm({ owner, repo, release, onDone, onCancel }: { owner: string; repo: string; release: Release | null; onDone: () => void; onCancel: () => void }) {
  const [tagName, setTagName] = useState(release?.tagName ?? '');
  const [title, setTitle] = useState(release?.name ?? '');
  const [body, setBody] = useState(release?.body ?? '');
  const [generate, setGenerate] = useState(false);
  const [prerelease, setPrerelease] = useState(release?.prerelease ?? false);
  const [draft, setDraft] = useState(release?.draft ?? false);
  const [files, setFiles] = useState<File[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    let saved: Release;
    try {
      saved = release
        ? await updateRelease(owner, repo, release.id, { name: title.trim(), body, draft, prerelease })
        : await createRelease(owner, repo, { tagName: tagName.trim(), name: title.trim() || tagName.trim(), body, draft, prerelease, generateNotes: generate });
    } catch (err) {
      setError(describeError(err));
      setBusy(false);
      return;
    }
    for (const f of files) {
      try {
        await uploadReleaseAsset(owner, repo, saved.id, f);
      } catch (err) {
        setError(`The release was saved, but ${f.name} was not uploaded: ${describeError(err)}`);
        setBusy(false);
        return;
      }
    }
    setBusy(false);
    onDone();
  }

  return (
    <form className="card card-b stack" aria-label={release ? 'Edit release' : 'New release'} onSubmit={(e) => void submit(e)}>
      <h3>{release ? `Edit ${release.tagName}` : 'Draft a new release'}</h3>
      {error ? <ErrorAlert message={error} /> : null}
      <FormField label="Tag" hint={release ? 'The tag of a release cannot change.' : 'An existing tag of this repository.'}>
        <TextInput className="input mono" value={tagName} onChange={(e) => setTagName(e.target.value)} readOnly={release !== null} required placeholder="v1.0.0" />
      </FormField>
      <FormField label="Release title">
        <TextInput value={title} onChange={(e) => setTitle(e.target.value)} placeholder="GitStack 1.0.0" />
      </FormField>
      <div className="field">
        <label htmlFor="release-notes">Notes (Markdown)</label>
        <textarea id="release-notes" className="input" rows={8} value={body} onChange={(e) => setBody(e.target.value)} />
      </div>
      {release === null ? (
        <Checkbox label="Generate release notes" hint="Built from the commits and pull requests since the previous release." checked={generate} onCheckedChange={setGenerate} />
      ) : null}
      <Checkbox label="This is a pre-release" checked={prerelease} onCheckedChange={setPrerelease} />
      <Checkbox label="Save as draft" hint="Drafts are visible to writers only." checked={draft} onCheckedChange={setDraft} />
      <FormField label="Attach files" hint="Up to 2 GB each; the SHA-256 is computed by the server.">
        <input className="input" type="file" multiple onChange={(e) => setFiles(Array.from(e.target.files ?? []))} />
      </FormField>
      {files.length > 0 ? (
        <ul className="small muted" aria-label="Files to upload">
          {files.map((f) => (
            <li key={f.name}>
              {f.name} · {formatBytes(f.size)}
            </li>
          ))}
        </ul>
      ) : null}
      <div className="row">
        <Button type="submit" variant="primary" disabled={busy || !tagName.trim()}>
          {release ? 'Save release' : draft ? 'Save draft' : 'Publish release'}
        </Button>
        <Button onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
