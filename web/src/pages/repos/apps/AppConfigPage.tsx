import { HardDrive, Key, Lock, Plus, Settings, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, FormField, PasswordInput, TextInput, useToast } from '../../../components';
import { fetchAppSecrets, fetchAppVariables, removeApp, removeAppSecret, removeAppVariable, saveAppSecret, saveAppVariable } from '../../../lib/appsApi';
import type { App } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { timeAgo } from '../../../lib/format';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';
import { AppHeader } from './AppHeader';
import { appsPath } from './appMode';
import { Link, useNavigate } from 'react-router-dom';

const KEY_PATTERN = /^[A-Z_][A-Z0-9_]*$/;

export function AppConfigPage({ repo, app, perms }: { repo: Repository; app: App; perms: AppPermissions }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const navigate = useNavigate();
  const { data, loading, error, reload: reloadData } = useLoad(async () => {
    if (!perms.canReadConfig) return null;
    const [variables, secrets] = await Promise.all([
      fetchAppVariables(owner, name, app.name),
      perms.canConfigure ? fetchAppSecrets(owner, name, app.name) : Promise.resolve(null),
    ]);
    return { variables, secrets };
  });
  const [reload, setReload] = useState(0);
  const [form, setForm] = useState<{ kind: 'variable' | 'secret'; name?: string; value?: string } | null>(null);
  const [target, setTarget] = useState<{ kind: 'variable' | 'secret'; name: string } | null>(null);
  const [deleteAppOpen, setDeleteAppOpen] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  useEffect(() => { if (reload > 0) void reloadData(); }, [reload, reloadData]);

  async function remove() {
    if (!target) return;
    try {
      if (target.kind === 'secret') await removeAppSecret(owner, name, app.name, target.name);
      else await removeAppVariable(owner, name, app.name, target.name);
      setTarget(null); setReload((v) => v + 1);
    } catch (err) { setActionError(describeError(err)); }
  }

  async function confirmDeleteApp() {
    try {
      await removeApp(owner, name, app.name);
      navigate(appsPath(owner, name));
    } catch (err) { setActionError(describeError(err)); }
  }

  return <>
    <AppHeader repo={repo} app={app} mode="app-config" />
    <main className="page">
      {!perms.canReadConfig ? <p className="muted">Variables and secrets require write access to this repository.</p> : <div className="settings-grid">
        <nav className="subnav" aria-label="App settings">
          <span className="subnav-disabled" aria-disabled="true"><Settings size={16} aria-hidden="true" />General</span>
          <a className="active" href={appsPath(owner, name, app.name, 'settings')} aria-current="page"><Key size={16} aria-hidden="true" />Variables &amp; secrets</a>
          <Link to={appsPath(owner, name, app.name, 'settings/access')}><Key size={16} aria-hidden="true" />Access &amp; domains</Link>
          <span className="subnav-disabled" aria-disabled="true"><HardDrive size={16} aria-hidden="true" />Volumes &amp; resources</span>
          {perms.canDelete ? <><div className="grp">Danger zone</div><Button variant="danger" size="sm" onClick={() => setDeleteAppOpen(true)}><Trash2 size={14} aria-hidden="true" />Delete app</Button></> : null}
        </nav>
        <div className="stack" style={{ gap: 24 }}>
          {loading && data === null ? <p className="muted">Loading variables and secrets…</p> : null}
          {error ? <ErrorAlert message={error} /> : null}{actionError ? <ErrorAlert message={actionError} /> : null}
          {data ? <>
            <section>
              <div className="sec-h"><h2>Variables</h2><span className="sp" />{perms.canConfigure ? <Button size="sm" onClick={() => setForm(form?.kind === 'variable' ? null : { kind: 'variable' })}><Plus size={14} aria-hidden="true" />New variable</Button> : null}</div>
              {form?.kind === 'variable' ? <EntryForm key={`var-${form.name ?? 'new'}`} kind="variable" initialName={form.name} initialValue={form.value} onCancel={() => setForm(null)} onSaved={() => { setForm(null); setReload((v) => v + 1); }} save={(key, value) => saveAppVariable(owner, name, app.name, key, value)} /> : null}
              {data.variables.items.length === 0 ? <EmptyState title="No variables yet" description="Variables are readable by people with write access." /> : <div className="list">{data.variables.items.map((v) => <div className="list-row" key={v.name}><span className="mono" style={{ width: 220 }}>{v.name}</span><span className="mono muted" style={{ flex: 1 }}>{v.value}</span>{v.managed ? <span className="badge">set by GitStack</span> : perms.canConfigure ? <><Button size="sm" variant="ghost" aria-label={`Edit variable ${v.name}`} onClick={() => setForm({ kind: 'variable', name: v.name, value: v.value })}>Edit</Button><Button size="sm" variant="ghost" aria-label={`Delete variable ${v.name}`} onClick={() => setTarget({ kind: 'variable', name: v.name })}>Delete</Button></> : null}</div>)}</div>}
            </section>
            <section>
              {perms.canConfigure ? <><div className="sec-h"><h2>Secrets</h2><span className="badge">admin only</span><span className="sp" /><Button variant="primary" size="sm" onClick={() => setForm(form?.kind === 'secret' ? null : { kind: 'secret' })}><Plus size={14} aria-hidden="true" />New secret</Button></div>
              {form?.kind === 'secret' ? <EntryForm key={`secret-${form.name ?? 'new'}`} kind="secret" initialName={form.name} onCancel={() => setForm(null)} onSaved={() => { setForm(null); setReload((v) => v + 1); }} save={(key, value) => saveAppSecret(owner, name, app.name, key, value)} /> : null}
              {data.secrets?.items.length === 0 ? <EmptyState title="No secrets yet" description="Add a secret. Its value is never shown again." /> : <div className="list">{data.secrets?.items.map((s) => <div className="list-row" key={s.name}><Lock size={16} className="muted" aria-hidden="true" /><span className="mono" style={{ flex: 1 }}>{s.name}</span><span className="small muted">updated {timeAgo(s.updatedAt)}{s.updatedBy?.username ? ` by ${s.updatedBy.username}` : ''}</span><Button size="sm" onClick={() => setForm({ kind: 'secret', name: s.name })}>Update</Button><Button size="sm" variant="ghost" aria-label={`Delete secret ${s.name}`} onClick={() => setTarget({ kind: 'secret', name: s.name })}>Delete</Button></div>)}</div>}
              <p className="hint" style={{ marginTop: 8 }}>Values can&apos;t be read back and are masked as <span className="mono">***</span> in logs. App secrets are separate from repository Actions secrets.</p></> : <p className="hint">Secrets and their metadata are visible only to repository admins.</p>}
            </section>
            <div className="alert alert-info">Saving creates a new version (image + configuration). Copies restart one at a time; you can roll back from <Link to={appsPath(owner, name, app.name, 'versions')}>Versions</Link>.</div>
          </> : null}
        </div>
      </div>}
    </main>
    <ConfirmDialog open={target !== null} onOpenChange={(open) => { if (!open) setTarget(null); }} title={`Delete ${target?.kind ?? ''}?`} description={`${target?.name ?? ''} will be removed from the app configuration.`} confirmLabel="Delete" destructive onConfirm={() => void remove()} />
    <ConfirmDialog open={deleteAppOpen} onOpenChange={setDeleteAppOpen} title={`Delete ${app.name}?`} description="This stops the app and removes its configuration." confirmLabel="Delete app" destructive onConfirm={() => void confirmDeleteApp()} />
  </>;
}

function EntryForm({ kind, save, onCancel, onSaved, initialName, initialValue = '' }: { kind: 'variable' | 'secret'; save: (name: string, value: string) => Promise<void>; onCancel: () => void; onSaved: () => void; initialName?: string; initialValue?: string }) {
  const [key, setKey] = useState(initialName ?? '');
  const [value, setValue] = useState(initialValue);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!KEY_PATTERN.test(key)) { setError('Use an uppercase environment variable name, such as DATABASE_URL.'); return; }
    setBusy(true); setError(null);
    try {
      await save(key, value);
      // Never retain or render the secret value after the write completes.
      setValue('');
      if (kind === 'secret') toast('Secret saved. Its value cannot be read back.', 'success');
      onSaved();
    } catch (err) { setError(describeError(err)); }
    finally { setBusy(false); }
  }
  return <form className="card stack" onSubmit={(e) => void submit(e)} style={{ padding: 16 }}>
    <FormField label="Name"><TextInput autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} required readOnly={initialName !== undefined} /></FormField>
    <FormField label="Value">{kind === 'secret' ? <PasswordInput autoComplete="new-password" value={value} onChange={(e) => setValue(e.target.value)} required /> : <TextInput autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} required />}</FormField>
    {error ? <ErrorAlert message={error} /> : null}
    <div className="row"><Button type="submit" variant="primary" disabled={busy || !value}>Save {kind}</Button><Button onClick={onCancel} disabled={busy}>Cancel</Button></div>
  </form>;
}
