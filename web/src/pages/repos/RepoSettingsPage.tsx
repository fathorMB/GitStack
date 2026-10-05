import { Lock, Shield } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Button, Checkbox, ConfirmDialog, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { deleteRepo, fetchRepo, updateRepo } from '../../lib/reposApi';
import type { RepoVisibility, Repository, UpdateRepositoryInput } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { VisibilityBadge } from './ReposPage';
import { isRepoAdmin, loadMe } from './repoAdmin';

// Impostazioni del repo, sezione General (mockup 18): solo per chi ha admin.
// Su un repo archiviato (R10) tutto e' in sola lettura tranne «Unarchive».
export function RepoSettingsPage() {
  const { owner = '', repo = '' } = useParams();
  return <RepoSettingsView key={`${owner}/${repo}`} owner={owner} repo={repo} />;
}

function RepoSettingsView({ owner, repo }: { owner: string; repo: string }) {
  const { data, setData, loading, error } = useLoad(async () => {
    const [r, me] = await Promise.all([fetchRepo(owner, repo), loadMe()]);
    return { repo: r, admin: await isRepoAdmin(r, me) };
  });

  if (loading && data === null) return <p className="muted">Loading settings…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Repository not found.'} />;
  const { repo: current, admin } = data;

  return (
    <div>
      <div className="repo-title row">
        <h1>
          {current.owner.name} / <Link to={`/${current.owner.name}/${current.name}`}>{current.name}</Link>
        </h1>
        <VisibilityBadge visibility={current.visibility} />
        {current.archived ? <span className="badge badge-archived">Archived</span> : null}
      </div>
      <div className="stack section-gap" style={{ gap: 24 }}>
        {admin ? (
          <SettingsSections key={current.updatedAt} repo={current} onChange={(r) => setData({ repo: r, admin })} />
        ) : (
          <p className="muted">Settings are available only to repository admins.</p>
        )}
      </div>
    </div>
  );
}

// 409/422 del server: il messaggio va sul campo, come da indicazione del CTO.
function fieldMessage(err: unknown, field: string): string {
  if (err instanceof ApiError && (err.status === 409 || err.status === 422)) return fieldErrors(err)[field] ?? err.message;
  return describeError(err);
}

function SettingsSections({ repo, onChange }: { repo: Repository; onChange: (r: Repository) => void }) {
  const { toast } = useToast();
  const navigate = useNavigate();
  const readOnly = repo.archived;
  const [description, setDescription] = useState(repo.description);
  const [visibility, setVisibility] = useState<RepoVisibility>(repo.visibility);
  const [branch, setBranch] = useState(repo.defaultBranch);
  const [protect, setProtect] = useState(repo.protectDefaultBranch);
  const [busy, setBusy] = useState(false);
  const [generalError, setGeneralError] = useState('');
  const [branchError, setBranchError] = useState('');
  const [dangerError, setDangerError] = useState('');
  const [confirm, setConfirm] = useState<'archive' | 'delete' | null>(null);

  async function save(input: UpdateRepositoryInput, setError: (m: string) => void, field: string, message: string) {
    setBusy(true);
    setError('');
    try {
      const updated = await updateRepo(repo.owner.name, repo.name, input);
      toast(message, 'success');
      onChange(updated);
    } catch (err) {
      setError(fieldMessage(err, field));
    } finally {
      setBusy(false);
    }
  }

  function submitGeneral(e: FormEvent) {
    e.preventDefault();
    void save({ description, visibility }, setGeneralError, 'description', 'Settings saved.');
  }
  function submitBranch(e: FormEvent) {
    e.preventDefault();
    void save({ defaultBranch: branch.trim(), protectDefaultBranch: protect }, setBranchError, 'defaultBranch', 'Branch settings saved.');
  }
  function setArchived(archived: boolean) {
    void save({ archived }, setDangerError, 'archived', archived ? 'Repository archived.' : 'Repository unarchived.');
  }
  async function remove() {
    try {
      await deleteRepo(repo.owner.name, repo.name);
      toast('Repository deleted. You can restore it for 7 days.', 'success');
      navigate('/repos');
    } catch (err) {
      setDangerError(describeError(err));
    }
  }

  return (
    <>
      {readOnly ? (
        <div className="alert alert-info" role="status">
          This repository is archived: settings are read-only. Unarchive it to change them.
        </div>
      ) : null}

      <section aria-label="General">
        <div className="sec-h">
          <h2>General</h2>
        </div>
        <form className="card card-b stack form-stack" onSubmit={submitGeneral}>
          <FormField label="Repository" hint="Owner and name are fixed for the life of the repository.">
            <TextInput className="mono" value={repo.fullName} readOnly disabled />
          </FormField>
          <FormField label="Description" error={generalError}>
            <TextInput value={description} maxLength={1024} disabled={readOnly} onChange={(e) => setDescription(e.target.value)} />
          </FormField>
          <fieldset className="scope-set" disabled={readOnly}>
            <legend>Visibility</legend>
            <div className="stack" style={{ gap: 8 }}>
              <label className="check scope">
                <input type="radio" name="visibility" value="private" checked={visibility === 'private'} onChange={() => setVisibility('private')} />
                <span>
                  <b>
                    <Lock size={14} aria-hidden="true" /> Private
                  </b>
                  <br />
                  <span className="muted small">Only organization owners and the people and teams you add can see it.</span>
                </span>
              </label>
              <label className="check scope">
                <input type="radio" name="visibility" value="internal" checked={visibility === 'internal'} onChange={() => setVisibility('internal')} />
                <span>
                  <b>
                    <Shield size={14} aria-hidden="true" /> Internal
                  </b>
                  <br />
                  <span className="muted small">Everyone signed in to this GitStack can see it.</span>
                </span>
              </label>
            </div>
          </fieldset>
          <div className="row">
            <span className="sp" />
            <Button type="submit" variant="primary" disabled={readOnly || busy}>
              Save changes
            </Button>
          </div>
        </form>
      </section>

      <section aria-label="Branch settings">
        <div className="sec-h">
          <h2>Default branch</h2>
        </div>
        <form className="card card-b stack form-stack" onSubmit={submitBranch}>
          <p className="muted">
            The default branch is shown first in the code browser and is where <span className="mono">fixes #12</span> closes issues.
          </p>
          <FormField label="Default branch" hint="Enter the name of an existing branch to make it the default." error={branchError}>
            <TextInput className="mono" value={branch} disabled={readOnly} onChange={(e) => setBranch(e.target.value)} />
          </FormField>
          <Checkbox
            label="Protect the default branch"
            hint={`Reject force-pushes and deletion of ${repo.defaultBranch}. Recommended, especially when coding agents have write access.`}
            checked={protect}
            disabled={readOnly}
            onCheckedChange={(v) => setProtect(v === true)}
          />
          <div className="row">
            <span className="sp" />
            <Button type="submit" variant="primary" disabled={readOnly || busy || branch.trim() === ''}>
              Save branch settings
            </Button>
          </div>
        </form>
      </section>

      <section aria-label="Danger zone">
        <div className="sec-h">
          <h2 style={{ color: 'var(--danger)' }}>Danger zone</h2>
        </div>
        {dangerError ? <ErrorAlert message={dangerError} /> : null}
        <div className="list" style={{ borderColor: 'var(--danger)' }}>
          {repo.archived ? (
            <div className="list-row" style={{ padding: '14px 16px' }}>
              <div style={{ flex: 1 }}>
                <b>Unarchive this repository</b>
                <div className="small muted">Make it writable again: pushes and settings changes are accepted.</div>
              </div>
              <Button disabled={busy} onClick={() => setArchived(false)}>
                Unarchive
              </Button>
            </div>
          ) : (
            <div className="list-row" style={{ padding: '14px 16px' }}>
              <div style={{ flex: 1 }}>
                <b>Archive this repository</b>
                <div className="small muted">
                  Make it read-only: it stays visible and clonable, but pushes and settings changes are rejected. You can unarchive it at any time.
                </div>
              </div>
              <Button variant="danger" disabled={busy} onClick={() => setConfirm('archive')}>
                Archive
              </Button>
            </div>
          )}
          <div className="list-row" style={{ padding: '14px 16px' }}>
            <div style={{ flex: 1 }}>
              <b>Delete this repository</b>
              <div className="small muted">
                It disappears immediately for everyone. Admins can restore it for 7 days from Deleted repositories; after that it is removed permanently.
                The name stays reserved until then.
              </div>
            </div>
            <Button variant="danger" disabled={readOnly || busy} onClick={() => setConfirm('delete')}>
              Delete
            </Button>
          </div>
        </div>
      </section>

      <ConfirmDialog
        open={confirm === 'archive'}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={`Archive ${repo.fullName}?`}
        description="The repository becomes read-only: pushes and settings changes are rejected until you unarchive it."
        confirmLabel="Archive repository"
        destructive
        onConfirm={() => setArchived(true)}
      />
      <ConfirmDialog
        open={confirm === 'delete'}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={`Delete ${repo.fullName}?`}
        description="The repository disappears immediately for everyone. For 7 days an admin can restore it from Deleted repositories; after that it is removed permanently. The name stays reserved until then."
        confirmLabel="Delete repository"
        destructive
        onConfirm={() => void remove()}
      />
    </>
  );
}
