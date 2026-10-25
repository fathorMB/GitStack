import { Box, Key, Lock, ShieldCheck, X } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { NavLink, useParams } from 'react-router-dom';
import { Button, Checkbox, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { fetchPackageSettings, savePackageSettings } from '../../lib/packagesApi';
import type { PackageSettings } from '../../lib/packagesApi';
import { fetchRepo } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { RepoHeader } from './RepoHeader';
import { isRepoAdmin, loadMe } from './repoAdmin';

// Impostazioni del repo, sezione Packages (RG2, mockup B6): pulizia, tag protetti e backup. Solo per chi ha admin.
export function PackageSettingsPage() {
  const { owner = '', repo = '' } = useParams();
  return <PackageSettingsLoader key={`${owner}/${repo}`} owner={owner} repo={repo} />;
}

function PackageSettingsLoader({ owner, repo }: { owner: string; repo: string }) {
  const { data, error, loading } = useLoad(async () => {
    const [r, me] = await Promise.all([fetchRepo(owner, repo), loadMe()]);
    return { repo: r, admin: await isRepoAdmin(r, me) };
  });
  if (loading && data === null) return <p className="muted">Loading settings…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Repository not found.'} />;

  return (
    <div>
      <RepoHeader repo={data.repo} mode="settings" admin={data.admin} />
      {!data.admin ? (
        <p className="muted">Package settings are available only to repository admins.</p>
      ) : (
        <div className="settings-grid">
          <nav className="subnav" aria-label="Repository settings">
            <NavLink to={`/${owner}/${repo}/settings`}>
              <Key size={16} aria-hidden="true" />
              General
            </NavLink>
            <NavLink to={`/${owner}/${repo}/webhooks`}>
              <ShieldCheck size={16} aria-hidden="true" />
              Webhooks
            </NavLink>
            <div className="grp">Actions</div>
            <NavLink to={`/${owner}/${repo}/settings/secrets`}>
              <Lock size={16} aria-hidden="true" />
              Secrets &amp; variables
            </NavLink>
            <div className="grp">Registry</div>
            <NavLink to={`/${owner}/${repo}/settings/packages`} className="active">
              <Box size={16} aria-hidden="true" />
              Packages
            </NavLink>
          </nav>
          <div className="stack" style={{ gap: 24 }}>
            <PackageSettingsSections repo={data.repo} />
          </div>
        </div>
      )}
    </div>
  );
}

function PackageSettingsSections({ repo }: { repo: Repository }) {
  const settings = useLoad(() => fetchPackageSettings(repo.owner.name, repo.name));
  if (settings.loading && settings.data === null) return <p className="muted">Loading package settings…</p>;
  if (settings.error || !settings.data) return <ErrorAlert message={settings.error ?? 'Package settings not available.'} />;
  return <PackageSettingsForm repo={repo} initial={settings.data} />;
}

function toInt(v: string): number | undefined {
  const n = Number(v);
  return v.trim() !== '' && Number.isInteger(n) && n >= 1 ? n : undefined;
}

function PackageSettingsForm({ repo, initial }: { repo: Repository; initial: PackageSettings }) {
  const { toast } = useToast();
  const readOnly = repo.archived;
  const [patterns, setPatterns] = useState<string[]>(initial.protectedTagPatterns);
  const [draft, setDraft] = useState('');
  const [untagged, setUntagged] = useState(String(initial.deleteUntaggedAfterDays));
  const [keepLast, setKeepLast] = useState(initial.keepLastTags !== undefined ? String(initial.keepLastTags) : '');
  const [shaDays, setShaDays] = useState(initial.deleteShaTagsAfterDays !== undefined ? String(initial.deleteShaTagsAfterDays) : '');
  const [backup, setBackup] = useState(initial.includeInBackup);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  function add() {
    const p = draft.trim();
    setDraft('');
    if (p === '' || patterns.includes(p)) return;
    setPatterns([...patterns, p]);
  }
  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    // Invio aggiunge il tag senza inviare il form.
    if (e.key === 'Enter') {
      e.preventDefault();
      add();
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    const days = toInt(untagged);
    if (days === undefined) {
      setError('Delete untagged images after: enter a whole number of days, 1 or more.');
      return;
    }
    if (keepLast.trim() !== '' && toInt(keepLast) === undefined) {
      setError('Keep the last tags: enter a whole number, 1 or more, or leave it empty.');
      return;
    }
    if (shaDays.trim() !== '' && toInt(shaDays) === undefined) {
      setError('Delete sha-* tags after: enter a whole number of days, 1 or more, or leave it empty.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const saved = await savePackageSettings(repo.owner.name, repo.name, {
        protectedTagPatterns: patterns,
        deleteUntaggedAfterDays: days,
        includeInBackup: backup,
        ...(toInt(keepLast) !== undefined ? { keepLastTags: toInt(keepLast) } : {}),
        ...(toInt(shaDays) !== undefined ? { deleteShaTagsAfterDays: toInt(shaDays) } : {}),
      });
      setPatterns(saved.protectedTagPatterns);
      toast('Package settings saved.', 'success');
    } catch (err) {
      setError(err instanceof ApiError && (err.status === 400 || err.status === 422) ? (Object.values(fieldErrors(err))[0] ?? err.message) : describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="stack" style={{ gap: 24 }} onSubmit={submit}>
      {readOnly ? (
        <div className="alert alert-info" role="status">
          This repository is archived: package settings are read-only. Unarchive it to change them.
        </div>
      ) : null}
      <section aria-label="Protected tags">
        <div className="sec-h">
          <h2>Protected tags</h2>
        </div>
        <div className="card card-b stack form-stack">
          <p className="muted">Protected tags can&apos;t be overwritten or deleted, and cleanup never removes them.</p>
          <ul aria-label="Protected tag patterns" className="row" style={{ gap: 6, flexWrap: 'wrap', listStyle: 'none', padding: 0, margin: 0 }}>
            {patterns.map((p) => (
              <li key={p}>
                <span className="badge mono">
                  {p}
                  {readOnly ? null : (
                    <Button variant="ghost" size="sm" aria-label={`Remove ${p}`} onClick={() => setPatterns(patterns.filter((x) => x !== p))}>
                      <X size={12} aria-hidden="true" />
                    </Button>
                  )}
                </span>
              </li>
            ))}
          </ul>
          <div className="row" style={{ gap: 6 }}>
            <TextInput
              aria-label="Tag pattern"
              style={{ width: 220 }}
              placeholder="Add a tag, e.g. v* or latest"
              value={draft}
              disabled={readOnly}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={onKey}
            />
            <Button size="sm" disabled={readOnly || draft.trim() === ''} onClick={add}>
              Add
            </Button>
          </div>
        </div>
      </section>
      <section aria-label="Cleanup">
        <div className="sec-h">
          <h2>Cleanup</h2>
        </div>
        <div className="card card-b stack form-stack">
          <FormField label="Delete untagged images after (days)" hint="Images without a tag are deleted after this time. Tagged images are never deleted by this rule.">
            <TextInput type="number" min={1} style={{ width: 120 }} value={untagged} disabled={readOnly} onChange={(e) => setUntagged(e.target.value)} />
          </FormField>
          <FormField label="Keep only the last tags per image" hint="Optional. Protected tags always stay. Empty means no limit.">
            <TextInput type="number" min={1} style={{ width: 120 }} value={keepLast} disabled={readOnly} onChange={(e) => setKeepLast(e.target.value)} />
          </FormField>
          <FormField label="Delete sha-* tags after (days)" hint="Optional. Protected tags always stay. Empty means never.">
            <TextInput type="number" min={1} style={{ width: 120 }} value={shaDays} disabled={readOnly} onChange={(e) => setShaDays(e.target.value)} />
          </FormField>
        </div>
      </section>
      <section aria-label="Backups">
        <div className="sec-h">
          <h2>Backups</h2>
        </div>
        <div className="card card-b stack form-stack">
          <Checkbox
            label="Include this repository's images in backups"
            hint="Off by default: images can be rebuilt by CI, and they can be large."
            checked={backup}
            disabled={readOnly}
            onCheckedChange={(v) => setBackup(v === true)}
          />
        </div>
      </section>
      {error ? (
        <p role="alert" className="hint" style={{ color: 'var(--danger)', margin: 0 }}>
          {error}
        </p>
      ) : null}
      <div className="row">
        <span className="sp" />
        <Button type="submit" variant="primary" disabled={readOnly || busy}>
          Save package settings
        </Button>
      </div>
    </form>
  );
}
