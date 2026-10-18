import { Clock, Globe, Info, Lock, MirrorRound, Shield } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Checkbox,
  ErrorAlert,
  FormField,
  PasswordInput,
  Select,
  TextInput,
  useToast,
} from '../../components';
import { fetchSession } from '../../lib/authApi';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { fetchOrganizations } from '../../lib/orgsApi';
import {
  GITIGNORE_TEMPLATES,
  LICENSE_TEMPLATES,
  createRepo,
  validateRepoName,
} from '../../lib/reposApi';
import type { CreateRepositoryInput, RepoVisibility } from '../../lib/reposApi';
import { importRepo } from '../../lib/pullMirrorsApi';
import type { ImportRepositoryInput } from '../../lib/pullMirrorsApi';
import { useLoad } from '../../lib/useLoad';
import { NameInTrashNotice, nameInTrashInfo } from './NameInTrashNotice';
import type { NameInTrashInfo } from './NameInTrashNotice';
import { repoPath } from './ReposPage';

async function loadOwners(): Promise<string[]> {
  const [session, orgs] = await Promise.all([fetchSession(), fetchOrganizations()]);
  return [session.user.username, ...orgs.map((o) => o.name)];
}

type NewRepoMode = 'empty' | 'import';

const INTERVAL_OPTIONS = [
  { value: 300, label: '5 minutes' },
  { value: 600, label: '10 minutes' },
  { value: 900, label: '15 minutes' },
  { value: 1800, label: '30 minutes' },
  { value: 3600, label: '1 hour' },
  { value: 7200, label: '2 hours' },
  { value: 14400, label: '4 hours' },
  { value: 28800, label: '8 hours' },
  { value: 43200, label: '12 hours' },
  { value: 86400, label: '24 hours' },
] as const;

function validateSourceUrl(url: string): string {
  if (url.trim() === '') return 'Enter the source URL.';
  if (!/^https?:\/\//i.test(url)) return 'The URL must start with http:// or https://.';
  try {
    const u = new URL(url);
    if (u.protocol === 'http:') return 'Only HTTPS sources are allowed. Use https://.';
    if (u.username || u.password) return "Don't put credentials in the URL: use the token field below.";
  } catch {
    return 'Enter a valid URL (for example https://github.com/owner/repo.git).';
  }
  return '';
}

export function NewRepoPage() {
  const owners = useLoad(loadOwners);
  const { toast } = useToast();
  const navigate = useNavigate();

  const [mode, setMode] = useState<NewRepoMode>('empty');
  const [ownerChoice, setOwnerChoice] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [touched, setTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [visibility, setVisibility] = useState<RepoVisibility>('private');
  const [readme, setReadme] = useState(false);
  const [defaultLabels, setDefaultLabels] = useState(true);
  const [gitignore, setGitignore] = useState(false);
  const [gitignoreTemplate, setGitignoreTemplate] = useState<string>(GITIGNORE_TEMPLATES[0]);
  const [license, setLicense] = useState(false);
  const [licenseTemplate, setLicenseTemplate] = useState<string>(LICENSE_TEMPLATES[0]);
  const [sourceUrl, setSourceUrl] = useState('');
  const [sourceUsername, setSourceUsername] = useState('');
  const [sourceToken, setSourceToken] = useState('');
  const [mirrorMode, setMirrorMode] = useState<'mirror' | 'once'>('mirror');
  const [intervalSeconds, setIntervalSeconds] = useState(600);
  const [formError, setFormError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [serverErrorCode, setServerErrorCode] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [inTrash, setInTrash] = useState<NameInTrashInfo | null>(null);

  const ownerList = owners.data ?? [];
  const owner = ownerChoice ?? ownerList[0] ?? '';
  const nameError = touched || name !== '' ? validateRepoName(name) : '';
  const urlError = validateSourceUrl(sourceUrl);
  const mirrorValid = sourceUrl.trim() !== '' && urlError === '';
  const importReady = mirrorValid && name !== '' && owner !== '';
  const intervalLabel = useMemo(() => {
    if (mirrorMode === 'once') return null;
    const found = INTERVAL_OPTIONS.find((o) => o.value === intervalSeconds);
    return found ? found.label : '10 minutes';
  }, [mirrorMode, intervalSeconds]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setTouched(true);
    setFormError(null);
    setFields({});
    setServerErrorCode(null);
    setInTrash(null);

    if (mode === 'empty') {
      if (validateRepoName(name) !== '' || owner === '') return;
      const input: CreateRepositoryInput = {
        owner, name, visibility, readme, defaultLabels,
        ...(description.trim() ? { description: description.trim() } : {}),
        ...(gitignore ? { gitignoreTemplate: gitignoreTemplate as CreateRepositoryInput['gitignoreTemplate'] } : {}),
        ...(license ? { licenseTemplate: licenseTemplate as CreateRepositoryInput['licenseTemplate'] } : {}),
      };
      setSubmitting(true);
      try {
        const repo = await createRepo(input);
        toast(`Repository "${repo.fullName}" created.`, 'success');
        void navigate(repoPath(repo));
      } catch (err) {
        const trashed = nameInTrashInfo(err);
        if (trashed) {
          setInTrash(trashed);
          return;
        }
        const perField = fieldErrors(err);
        setFields(perField);
        setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
      } finally {
        setSubmitting(false);
      }
      return;
    }

    if (validateRepoName(name) !== '' || owner === '' || !mirrorValid) return;
    const input: ImportRepositoryInput = {
      owner, name, visibility, url: sourceUrl.trim(), mode: mirrorMode,
      ...(mirrorMode === 'mirror' ? { intervalSeconds } : {}),
      ...(sourceUsername.trim() ? { username: sourceUsername.trim() } : {}),
      ...(sourceToken.trim() ? { token: sourceToken.trim() } : {}),
      ...(description.trim() ? { description: description.trim() } : {}),
    };
    setSubmitting(true);
    try {
      await importRepo(input);
      toast(`Repository "${owner}/${name}" importing.`, 'success');
      void navigate(`/${owner}/${name}`);
    } catch (err) {
      const trashed = nameInTrashInfo(err);
      if (trashed) {
        setInTrash(trashed);
        return;
      }
      const perField = fieldErrors(err);
      setFields(perField);
      if (err instanceof ApiError) {
        setServerErrorCode(err.code);
      }
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  const isImport = mode === 'import';

  return (
    <div className="page-narrow">
      <h1 className="page-title">Create a new repository</h1>
      <p className="muted">A repository holds your project&rsquo;s files, history and issues.</p>
      {owners.error ? <ErrorAlert message={owners.error} /> : null}

      <form className="card card-b stack form-stack section-gap" onSubmit={(e) => void handleSubmit(e)} noValidate aria-label="New repository">
        {formError ? <ErrorAlert message={formError} /> : null}
        {inTrash ? <NameInTrashNotice info={inTrash} name={name} currentUser={ownerList[0] ?? ''} /> : null}

        <div className="seg" style={{ marginBottom: 0 }}>
          <button type="button" className={mode === 'empty' ? 'active' : undefined} onClick={() => setMode('empty')}>Empty repository</button>
          <button type="button" className={mode === 'import' ? 'active' : undefined} onClick={() => setMode('import')}>
            <Globe size={14} aria-hidden="true" />
            Import from URL
          </button>
        </div>

        {isImport ? null : (
          <>
            <div className="row repo-name-row">
              <FormField label="Owner" error={fields.owner}>
                <Select key={ownerList.join(',')} value={owner} onValueChange={setOwnerChoice} options={ownerList.map((o) => ({ value: o, label: o }))} placeholder="Select an owner" />
              </FormField>
              <span className="repo-slash" aria-hidden="true">/</span>
              <div className="grow">
                <FormField label="Repository name" hint="Lowercase letters, digits, - _ . — up to 100 characters." error={fields.name || nameError}>
                  <TextInput value={name} onChange={(e) => setName(e.target.value)} onBlur={() => setTouched(true)} placeholder="billing-service" autoComplete="off" />
                </FormField>
              </div>
            </div>

            <div className="alert alert-info" role="note">
              <Info size={16} aria-hidden="true" />
              <div><b>Choose the owner carefully.</b> Owner and name can&rsquo;t be changed later: to move a repository, create a new one and push the code there.</div>
            </div>

            <FormField label="Description (optional)" error={fields.description}>
              <TextInput value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What is this repository for?" />
            </FormField>

            <fieldset className="scope-set">
              <legend>Visibility</legend>
              <div className="stack form-stack">
                <label className={`check scope${visibility === 'private' ? ' selected' : ''}`}>
                  <input type="radio" name="visibility" value="private" checked={visibility === 'private'} onChange={() => setVisibility('private')} />
                  <span><b><Lock size={14} aria-hidden="true" /> Private</b><br /><span className="muted small">Only organization owners and the people and teams you add can see it.</span></span>
                </label>
                <label className={`check scope${visibility === 'internal' ? ' selected' : ''}`}>
                  <input type="radio" name="visibility" value="internal" checked={visibility === 'internal'} onChange={() => setVisibility('internal')} />
                  <span><b><Shield size={14} aria-hidden="true" /> Internal</b><br /><span className="muted small">Everyone signed in to this GitStack can see it.</span></span>
                </label>
              </div>
            </fieldset>

            <fieldset className="scope-set">
              <legend>Initialize</legend>
              <p className="hint">Leave these unchecked to push an existing project. Any option creates a first commit on <span className="mono">main</span>.</p>
              <div className="stack form-stack">
                <Checkbox label="Add a README" checked={readme} onCheckedChange={setReadme} />
                <Checkbox label="Add .gitignore" checked={gitignore} onCheckedChange={setGitignore} />
                {gitignore ? (
                  <FormField label="Template" error={fields.gitignoreTemplate}>
                    <Select value={gitignoreTemplate} onValueChange={setGitignoreTemplate} options={GITIGNORE_TEMPLATES.map((t) => ({ value: t, label: t }))} />
                  </FormField>
                ) : null}
                <Checkbox label="Add a license" checked={license} onCheckedChange={setLicense} />
                {license ? (
                  <FormField label="License" error={fields.licenseTemplate}>
                    <Select value={licenseTemplate} onValueChange={setLicenseTemplate} options={LICENSE_TEMPLATES.map((t) => ({ value: t, label: t }))} />
                  </FormField>
                ) : null}
              </div>
            </fieldset>

            <fieldset className="scope-set">
              <legend>Issues</legend>
              <Checkbox label="Add default labels" hint="bug, enhancement, documentation, question, duplicate, good first issue, agent-ready, needs-human." checked={defaultLabels} onCheckedChange={setDefaultLabels} />
            </fieldset>
          </>
        )}

        {isImport && (
          <>
            <div className="field">
              <label htmlFor="source-url">Source URL</label>
              <div className="input-icon">
                <Globe size={16} aria-hidden="true" />
                <TextInput id="source-url" className="input" value={sourceUrl} onChange={(e) => setSourceUrl(e.target.value)} placeholder="https://github.com/owner/repo.git" autoComplete="off" />
              </div>
              <span className="hint">HTTPS only. Don&rsquo;t put credentials in the URL: use the token field below.</span>
              {(urlError || fields.url) && (
                <span className="error" role="alert">{urlError || fields.url}</span>
              )}
              {serverErrorCode === 'url_not_allowed' && (
                <div className="alert alert-danger" role="alert" style={{ marginTop: 8 }}>
                  <Info size={16} aria-hidden="true" />
                  <div><b>This address is blocked by the outbound policy.</b> Private and internal addresses can only be used if an administrator allows them in <b>System · Outbound connections</b>.</div>
                </div>
              )}
            </div>

            <div className="row" style={{ gap: 10, flexWrap: 'nowrap' }}>
              <div className="field" style={{ flex: 1 }}>
                <label>Username <span className="subtle" style={{ fontWeight: 400 }}>(optional)</span></label>
                <TextInput value={sourceUsername} onChange={(e) => setSourceUsername(e.target.value)} placeholder="x-access-token" autoComplete="username" />
                {fields.username && <span className="error" role="alert" style={{ display: 'block' }}>{fields.username}</span>}
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>Access token <span className="subtle" style={{ fontWeight: 400 }}>(optional)</span></label>
                <PasswordInput value={sourceToken} onChange={(e) => setSourceToken(e.target.value)} placeholder="ghp_xxxxxxxxxxxxxxxx" autoComplete="token" />
                {fields.token && <span className="error" role="alert" style={{ display: 'block' }}>{fields.token}</span>}
              </div>
            </div>
            <p className="hint" style={{ marginTop: -10 }}>
              <Lock size={14} aria-hidden="true" /> Needed only for private sources. Stored encrypted; GitStack never shows it again.
            </p>

            <div className="row" style={{ alignItems: 'flex-end', gap: 10, flexWrap: 'nowrap' }}>
              <div className="field" style={{ width: 200 }}>
                <label>Owner</label>
                <Select key={ownerList.join(',')} value={owner} onValueChange={setOwnerChoice} options={ownerList.map((o) => ({ value: o, label: o }))} />
              </div>
              <span style={{ fontSize: 20, color: 'var(--text-subtle)', paddingBottom: 4 }}>/</span>
              <div className="field" style={{ flex: 1 }}>
                <FormField label="Repository name" error={fields.name || nameError}>
                  <TextInput value={name} onChange={(e) => setName(e.target.value)} onBlur={() => setTouched(true)} placeholder="repo-name" autoComplete="off" />
                </FormField>
              </div>
            </div>

            <div className="field">
              <label>Visibility</label>
              <label className={`check scope${visibility === 'private' ? ' selected' : ''}`} style={{ borderColor: 'var(--accent)', background: 'var(--accent-soft)' }}>
                <input type="radio" name="import-visibility" value="private" checked={visibility === 'private'} onChange={() => setVisibility('private')} />
                <span><b><Lock size={14} aria-hidden="true" /> Private</b> <span className="muted small">— default</span></span>
              </label>
              <label className={`check scope${visibility === 'internal' ? ' selected' : ''}`}>
                <input type="radio" name="import-visibility" value="internal" checked={visibility === 'internal'} onChange={() => setVisibility('internal')} />
                <span><b><Shield size={14} aria-hidden="true" /> Internal</b> <span className="muted small">Everyone signed in to this GitStack can see it.</span></span>
              </label>
            </div>

            <div className="field">
              <label>After the import</label>
              <label className={`check scope${mirrorMode === 'mirror' ? ' selected' : ''}`} style={{ borderColor: 'var(--accent)', background: 'var(--accent-soft)' }}>
                <input type="radio" name="import-mode" value="mirror" checked={mirrorMode === 'mirror'} onChange={() => setMirrorMode('mirror')} />
                <span>
                  <b><MirrorRound size={14} aria-hidden="true" /> Keep in sync (pull mirror)</b>
                  <br />
                  <span className="muted small">GitStack fetches all branches and tags from the source every <b>{intervalLabel}</b>. Code is <b>read-only</b> here; issues work normally. Nothing is ever deleted or overwritten: rewritten refs are flagged as <i>diverged</i>.</span>
                </span>
              </label>
              <label className={`check scope${mirrorMode === 'once' ? ' selected' : ''}`} style={mirrorMode === 'once' ? { borderColor: 'var(--accent)', background: 'var(--accent-soft)' } : {}}>
                <input type="radio" name="import-mode" value="once" checked={mirrorMode === 'once'} onChange={() => setMirrorMode('once')} />
                <span><b>Copy once</b><br /><span className="muted small">Import the history now and work on it here. No link with the source.</span></span>
              </label>

              {mirrorMode === 'mirror' && (
                <div className="row" style={{ marginTop: 8, gap: 8 }}>
                  <label className="muted small" style={{ whiteSpace: 'nowrap', display: 'flex', alignItems: 'center', gap: 4 }}>
                    <Clock size={14} aria-hidden="true" />
                    Refresh interval
                  </label>
                  <Select value={String(intervalSeconds)} onValueChange={(v) => setIntervalSeconds(Number(v))} options={INTERVAL_OPTIONS.map((o) => ({ value: String(o.value), label: o.label }))} />
                </div>
              )}
            </div>
          </>
        )}

        <div className="row repo-form-foot">
          <span className="muted small">Default branch: <span className="mono">main</span></span>
          <span className="sp" />
          <Button onClick={() => void navigate(-1)}>Cancel</Button>
          {isImport ? (
            <Button type="submit" variant="primary" disabled={!importReady || submitting || owners.loading}>
              {submitting ? 'Importing…' : 'Import repository'}
            </Button>
          ) : (
            <Button type="submit" variant="primary" disabled={submitting || owners.loading}>
              {submitting ? 'Creating…' : 'Create repository'}
            </Button>
          )}
        </div>
      </form>

      {isImport && (
        <p className="hint" style={{ marginTop: 12 }}>
          <span className="mtag">nota</span> «Copy once» is the one-time import (MP8): same origins and credentials, then the credentials are deleted and the repo is normal and writable.
        </p>
      )}
    </div>
  );
}
