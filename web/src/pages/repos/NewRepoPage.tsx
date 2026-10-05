import { Info, Lock, Shield } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Checkbox, ErrorAlert, FormField, Select, TextInput, useToast } from '../../components';
import { fetchSession } from '../../lib/authApi';
import { describeError, fieldErrors } from '../../lib/http';
import { fetchOrganizations } from '../../lib/orgsApi';
import { GITIGNORE_TEMPLATES, LICENSE_TEMPLATES, createRepo, validateRepoName } from '../../lib/reposApi';
import type { CreateRepositoryInput, RepoVisibility } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { repoPath } from './ReposPage';

// Owner possibili: l'utente corrente e le sue organizzazioni.
async function loadOwners(): Promise<string[]> {
  const [session, orgs] = await Promise.all([fetchSession(), fetchOrganizations()]);
  return [session.user.username, ...orgs.map((o) => o.name)];
}

// Nuovo repository (mockup 04): owner (R3: non cambia piu), nome validato
// come R11, visibilita private (default) o internal, contenuto iniziale
// tutto spento (R5).
export function NewRepoPage() {
  const owners = useLoad(loadOwners);
  const { toast } = useToast();
  const navigate = useNavigate();

  const [ownerChoice, setOwnerChoice] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [touched, setTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [visibility, setVisibility] = useState<RepoVisibility>('private');
  const [readme, setReadme] = useState(false);
  const [gitignore, setGitignore] = useState(false);
  const [gitignoreTemplate, setGitignoreTemplate] = useState<string>(GITIGNORE_TEMPLATES[0]);
  const [license, setLicense] = useState(false);
  const [licenseTemplate, setLicenseTemplate] = useState<string>(LICENSE_TEMPLATES[0]);
  const [formError, setFormError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  const ownerList = owners.data ?? [];
  const owner = ownerChoice ?? ownerList[0] ?? '';
  const nameError = touched || name !== '' ? validateRepoName(name) : '';

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setTouched(true);
    if (validateRepoName(name) !== '' || owner === '') return;
    const input: CreateRepositoryInput = {
      owner,
      name,
      visibility,
      readme,
      ...(description.trim() ? { description: description.trim() } : {}),
      ...(gitignore ? { gitignoreTemplate: gitignoreTemplate as CreateRepositoryInput['gitignoreTemplate'] } : {}),
      ...(license ? { licenseTemplate: licenseTemplate as CreateRepositoryInput['licenseTemplate'] } : {}),
    };
    setSubmitting(true);
    setFormError(null);
    setFields({});
    try {
      const repo = await createRepo(input);
      toast(`Repository "${repo.fullName}" created.`, 'success');
      void navigate(repoPath(repo));
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="page-narrow">
      <h1 className="page-title">Create a new repository</h1>
      <p className="muted">A repository holds your project&rsquo;s files, history and issues.</p>

      {owners.error ? <ErrorAlert message={owners.error} /> : null}

      <form className="card card-b stack form-stack section-gap" onSubmit={(e) => void handleSubmit(e)} noValidate aria-label="New repository">
        {formError ? <ErrorAlert message={formError} /> : null}

        <div className="row repo-name-row">
          <FormField label="Owner" error={fields.owner}>
            <Select
              key={ownerList.join(",")}
              value={owner}
              onValueChange={setOwnerChoice}
              options={ownerList.map((o) => ({ value: o, label: o }))}
              placeholder="Select an owner"
            />
          </FormField>
          <span className="repo-slash" aria-hidden="true">
            /
          </span>
          <div className="grow">
            <FormField
              label="Repository name"
              hint="Lowercase letters, digits, - _ . — up to 100 characters."
              error={nameError || fields.name}
            >
              <TextInput
                value={name}
                onChange={(e) => setName(e.target.value)}
                onBlur={() => setTouched(true)}
                placeholder="billing-service"
                autoComplete="off"
              />
            </FormField>
          </div>
        </div>

        <div className="alert alert-info" role="note">
          <Info size={16} aria-hidden="true" />
          <div>
            <b>Choose the owner carefully.</b> Owner and name can&rsquo;t be changed later: to move a repository, create a new one and
            push the code there.
          </div>
        </div>

        <FormField label="Description (optional)" error={fields.description}>
          <TextInput value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What is this repository for?" />
        </FormField>

        <fieldset className="scope-set">
          <legend>Visibility</legend>
          <div className="stack form-stack">
            <label className={`check scope${visibility === 'private' ? ' selected' : ''}`}>
              <input type="radio" name="visibility" value="private" checked={visibility === 'private'} onChange={() => setVisibility('private')} />
              <span>
                <b>
                  <Lock size={14} aria-hidden="true" /> Private
                </b>
                <br />
                <span className="muted small">Only organization owners and the people and teams you add can see it.</span>
              </span>
            </label>
            <label className={`check scope${visibility === 'internal' ? ' selected' : ''}`}>
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

        <fieldset className="scope-set">
          <legend>Initialize</legend>
          <p className="hint">
            Leave these unchecked to push an existing project. Any option creates a first commit on <span className="mono">main</span>.
          </p>
          <div className="stack form-stack">
            <Checkbox label="Add a README" checked={readme} onCheckedChange={setReadme} />
            <Checkbox label="Add .gitignore" checked={gitignore} onCheckedChange={setGitignore} />
            {gitignore ? (
              <FormField label="Template" error={fields.gitignoreTemplate}>
                <Select
                  value={gitignoreTemplate}
                  onValueChange={setGitignoreTemplate}
                  options={GITIGNORE_TEMPLATES.map((t) => ({ value: t, label: t }))}
                />
              </FormField>
            ) : null}
            <Checkbox label="Add a license" checked={license} onCheckedChange={setLicense} />
            {license ? (
              <FormField label="License" error={fields.licenseTemplate}>
                <Select
                  value={licenseTemplate}
                  onValueChange={setLicenseTemplate}
                  options={LICENSE_TEMPLATES.map((t) => ({ value: t, label: t }))}
                />
              </FormField>
            ) : null}
          </div>
        </fieldset>

        <div className="row repo-form-foot">
          <span className="muted small">
            Default branch: <span className="mono">main</span>
          </span>
          <span className="sp" />
          <Button onClick={() => void navigate(-1)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={submitting || owners.loading}>
            {submitting ? 'Creating…' : 'Create repository'}
          </Button>
        </div>
      </form>
    </div>
  );
}
