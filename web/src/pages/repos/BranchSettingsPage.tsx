import { Info, X } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { useParams } from 'react-router-dom';
import { Button, Checkbox, ErrorAlert, FormField, Select, TextInput, useToast } from '../../components';
import { fetchBranchSettings, saveBranchSettings } from '../../lib/branchSettingsApi';
import type { BranchSettings, UpdateBranchSettingsInput } from '../../lib/branchSettingsApi';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { fetchRepo, updateRepo } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { RepoHeader } from './RepoHeader';
import { isRepoAdmin, loadMe } from './repoAdmin';

// Impostazioni del repo, sezione «Branches & merge» (mockup 28, v1.1): solo per chi ha admin.
// Regole per il branch principale (R9, PR4, PR9) e metodi di merge (PR3).
// Su un repo archiviato (R10) tutto e' in sola lettura.
export function BranchSettingsPage() {
  const { owner = '', repo = '' } = useParams();
  return <BranchSettingsLoader key={`${owner}/${repo}`} owner={owner} repo={repo} />;
}

function BranchSettingsLoader({ owner, repo }: { owner: string; repo: string }) {
  const { data, error, loading } = useLoad(async () => {
    const [r, me] = await Promise.all([fetchRepo(owner, repo), loadMe()]);
    return { repo: r, admin: await isRepoAdmin(r, me) };
  });

  if (loading && data === null) return <p className="muted">Loading settings…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Repository not found.'} />;
  const { repo: current, admin } = data;

  return (
    <div>
      <RepoHeader repo={current} mode="settings" admin={admin} />
      <div className="stack section-gap" style={{ gap: 24 }}>
        {admin ? <BranchSettingsSections repo={current} /> : <p className="muted">Branches &amp; merge settings are available only to repository admins.</p>}
      </div>
    </div>
  );
}

function fieldMessage(err: unknown, field: string): string {
  if (err instanceof ApiError && (err.status === 409 || err.status === 422)) return fieldErrors(err)[field] ?? err.message;
  return describeError(err);
}

function BranchSettingsSections({ repo }: { repo: Repository }) {
  const settings = useLoad(() => fetchBranchSettings(repo.owner.name, repo.name));
  if (settings.loading && settings.data === null) return <p className="muted">Loading branch settings…</p>;
  if (settings.error || !settings.data) return <ErrorAlert message={settings.error ?? 'Branch settings not available.'} />;
  return <BranchSettingsForms repo={repo} initial={settings.data} onSaved={settings.setData} />;
}

function BranchSettingsForms({ repo, initial, onSaved }: { repo: Repository; initial: BranchSettings; onSaved: (s: BranchSettings) => void }) {
  const readOnly = repo.archived;
  return (
    <>
      {readOnly ? (
        <div className="alert alert-info" role="status">
          This repository is archived: branch settings are read-only. Unarchive it to change them.
        </div>
      ) : null}
      <RulesSection repo={repo} initial={initial} readOnly={readOnly} onSaved={onSaved} />
      <MergeSection repo={repo} initial={initial} readOnly={readOnly} onSaved={onSaved} />
    </>
  );
}

// Regole per il branch principale: protezione (R9) e «Require a pull request» (PR4, PR9).
function RulesSection({ repo, initial, readOnly, onSaved }: { repo: Repository; initial: BranchSettings; readOnly: boolean; onSaved: (s: BranchSettings) => void }) {
  const { toast } = useToast();
  const [protect, setProtect] = useState(repo.protectDefaultBranch);
  const [requirePr, setRequirePr] = useState(initial.requirePullRequest);
  const [approvals, setApprovals] = useState(String(initial.requiredApprovals));
  const [contexts, setContexts] = useState<string[]>(initial.requiredStatusContexts);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const input: UpdateBranchSettingsInput = {
        // I campi della sezione Merging restano quelli salvati: il PATCH li rimanda invariati.
        allowMergeCommit: initial.allowMergeCommit,
        allowSquashMerge: initial.allowSquashMerge,
        defaultMergeMethod: initial.defaultMergeMethod,
        deleteBranchOnMerge: initial.deleteBranchOnMerge,
        requirePullRequest: requirePr,
        requiredApprovals: Number(approvals),
        requiredStatusContexts: contexts,
      };
      const [settings] = await Promise.all([
        saveBranchSettings(repo.owner.name, repo.name, input),
        protect !== repo.protectDefaultBranch ? updateRepo(repo.owner.name, repo.name, { protectDefaultBranch: protect }) : Promise.resolve(null),
      ]);
      toast('Rules saved.', 'success');
      onSaved(settings);
    } catch (err) {
      setError(fieldMessage(err, 'requiredApprovals'));
    } finally {
      setBusy(false);
    }
  }

  const approvalOptions = [1, 2, 3, 4, 5].map((n) => ({ value: String(n), label: String(n) }));

  return (
    <section aria-label="Rules">
      <div className="sec-h">
        <h2>
          Rules for <span className="mono">{repo.defaultBranch}</span>
        </h2>
      </div>
      <form className="card card-b stack form-stack" onSubmit={submit}>
        <Checkbox
          label="Protect the default branch"
          hint={`Reject force-pushes and deletion of ${repo.defaultBranch}.`}
          checked={protect}
          disabled={readOnly}
          onCheckedChange={(v) => setProtect(v === true)}
        />
        <div style={{ height: 1, background: 'var(--border)' }} aria-hidden="true" />
        <Checkbox
          label="Require a pull request before merging"
          hint={`Direct pushes to ${repo.defaultBranch} are rejected for everyone, admins included. Changes arrive only through pull requests.`}
          checked={requirePr}
          disabled={readOnly}
          onCheckedChange={(v) => setRequirePr(v === true)}
        />
        <fieldset className="scope-set" disabled={readOnly || !requirePr} style={{ border: 0, padding: 0, margin: 0 }}>
          <div className="stack" style={{ gap: 14 }}>
            <FormField label="Required approvals from people" hint="Approvals from agent accounts are shown but never count. Authors can't approve their own pull request.">
              <Select aria-label="Required approvals from people" value={approvals} options={approvalOptions} onValueChange={setApprovals} />
            </FormField>
            <div className="row small" style={{ gap: 8 }}>
              <Info size={16} aria-hidden="true" style={{ color: 'var(--info)' }} />
              <span>New commits pushed after an approval dismiss it: the changes must be reviewed again.</span>
            </div>
            <StatusContexts value={contexts} disabled={readOnly || !requirePr} onChange={setContexts} />
          </div>
        </fieldset>
        {error ? (
          <p role="alert" className="hint" style={{ color: 'var(--danger)', margin: 0 }}>
            {error}
          </p>
        ) : null}
        <div className="alert alert-info" role="note">
          <Info size={16} aria-hidden="true" />
          <div>
            Turning this on doesn&apos;t affect an empty repository&apos;s first push: enable it after <span className="mono">{repo.defaultBranch}</span> exists. Off by
            default.
          </div>
        </div>
        <div className="row">
          <span className="sp" />
          <Button type="submit" variant="primary" disabled={readOnly || busy}>
            Save rules
          </Button>
        </div>
      </form>
    </section>
  );
}

// Contesti di stato obbligatori (PR9): chip rimovibili e un campo per aggiungerne uno.
function StatusContexts({ value, disabled, onChange }: { value: string[]; disabled: boolean; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState('');

  function add() {
    const c = draft.trim();
    setDraft('');
    if (c === '' || value.includes(c)) return;
    onChange([...value, c]);
  }
  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    // Invio aggiunge il contesto senza inviare il form.
    if (e.key === 'Enter') {
      e.preventDefault();
      add();
    }
  }

  return (
    <div className="field">
      <label htmlFor="required-contexts">Required status checks</label>
      <ul aria-label="Required status contexts" className="row" style={{ gap: 6, flexWrap: 'wrap', listStyle: 'none', padding: 0, margin: '6px 0' }}>
        {value.map((c) => (
          <li key={c}>
            <span className="badge mono">
              {c}
              {disabled ? null : (
                <Button variant="ghost" size="sm" aria-label={`Remove ${c}`} onClick={() => onChange(value.filter((x) => x !== c))}>
                  <X size={12} aria-hidden="true" />
                </Button>
              )}
            </span>
          </li>
        ))}
      </ul>
      <div className="row" style={{ gap: 6 }}>
        <TextInput
          id="required-contexts"
          style={{ width: 220 }}
          placeholder="Add a context, e.g. lint"
          value={draft}
          disabled={disabled}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKey}
        />
        <Button size="sm" disabled={disabled || draft.trim() === ''} onClick={add}>
          Add
        </Button>
      </div>
      <span className="hint">
        Each context must report <b>success</b> on the latest commit of the pull request. External CI systems report statuses with a token that has Write on this
        repository.
      </span>
    </div>
  );
}

// Metodi di merge (PR3) ed eliminazione automatica del branch.
function MergeSection({ repo, initial, readOnly, onSaved }: { repo: Repository; initial: BranchSettings; readOnly: boolean; onSaved: (s: BranchSettings) => void }) {
  const { toast } = useToast();
  const [squash, setSquash] = useState(initial.allowSquashMerge);
  const [merge, setMerge] = useState(initial.allowMergeCommit);
  const [deleteHead, setDeleteHead] = useState(initial.deleteBranchOnMerge);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!squash && !merge) {
      setError('At least one merge method stays enabled.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const settings = await saveBranchSettings(repo.owner.name, repo.name, {
        allowMergeCommit: merge,
        allowSquashMerge: squash,
        defaultMergeMethod: initial.defaultMergeMethod,
        deleteBranchOnMerge: deleteHead,
        requirePullRequest: initial.requirePullRequest,
        requiredApprovals: initial.requiredApprovals,
        requiredStatusContexts: initial.requiredStatusContexts,
      });
      toast('Merge settings saved.', 'success');
      onSaved(settings);
    } catch (err) {
      setError(fieldMessage(err, 'allowMergeCommit'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-label="Merging pull requests">
      <div className="sec-h">
        <h2>Merging pull requests</h2>
      </div>
      <form className="card card-b stack form-stack" onSubmit={submit}>
        <Checkbox
          label="Allow squash merging"
          hint="Combine all commits into one on the base branch. Proposed by default."
          checked={squash}
          disabled={readOnly}
          onCheckedChange={(v) => setSquash(v === true)}
        />
        <Checkbox
          label="Allow merge commits"
          hint="Keep all commits and add a merge commit. At least one method stays enabled."
          checked={merge}
          disabled={readOnly}
          onCheckedChange={(v) => setMerge(v === true)}
        />
        {error ? (
          <p role="alert" className="hint" style={{ color: 'var(--danger)', margin: 0 }}>
            {error}
          </p>
        ) : null}
        <div style={{ height: 1, background: 'var(--border)' }} aria-hidden="true" />
        <Checkbox
          label="Automatically delete head branches"
          hint={`Delete the pull request's branch after it is merged. The default branch (${repo.defaultBranch}) is never deleted.`}
          checked={deleteHead}
          disabled={readOnly}
          onCheckedChange={(v) => setDeleteHead(v === true)}
        />
        <div className="row">
          <span className="sp" />
          <Button type="submit" variant="primary" disabled={readOnly || busy}>
            Save
          </Button>
        </div>
      </form>
    </section>
  );
}
