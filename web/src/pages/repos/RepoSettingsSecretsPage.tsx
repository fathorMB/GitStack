import { Key, Lock, Plus, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { NavLink, useParams } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert, FormField, PasswordInput, TextInput, useToast } from '../../components';
import { fetchOrgSecrets, fetchRepoSecrets, fetchRepoVariables, putRepoSecret, putRepoVariable } from '../../lib/ciApi';
import { timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import { fetchRepo } from '../../lib/reposApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { RepoHeader } from './RepoHeader';
import { isRepoAdmin, loadMe } from './repoAdmin';

// Il contratto (CI4) espone i segreti come metadati: nome e data. Il valore e'
// `writeOnly` in OpenAPI, quindi qui non esiste nessun percorso che lo rileggi:
// si scrive una volta e poi si sostituisce. Nelle righe della lista puo' uscire
// solo quello che il server manda, per questo il test verifica che il valore
// scritto non ricompaia nella pagina.
const KEY_PATTERN = /^[A-Z_][A-Z0-9_]*$/;

// Settings · Secrets & variables del repo (mockup B3, CI4): solo admin, piu' i
// segreti ereditati dall'organizzazione. La sezione permessi del token del job e'
// in sola lettura: il contratto non ha nessuna operazione per cambiarla.
export function RepoSettingsSecretsPage() {
  const { owner = '', repo = '' } = useParams();
  return <SecretsView key={`${owner}/${repo}`} owner={owner} repo={repo} />;
}

function SecretsView({ owner, repo }: { owner: string; repo: string }) {
  const { data, loading, error } = useLoad(async () => {
    const [r, me] = await Promise.all([fetchRepo(owner, repo), loadMe()]);
    return { repo: r, admin: await isRepoAdmin(r, me) };
  });

  if (loading && data === null) return <p className="muted">Loading secrets…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Repository not found.'} />;

  return (
    <div>
      <RepoHeader repo={data.repo} mode="settings" admin={data.admin} />
      {!data.admin ? (
        <p className="muted">Settings are available only to repository admins.</p>
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
            <NavLink to={`/${owner}/${repo}/settings/secrets`} className="active">
              <Lock size={16} aria-hidden="true" />
              Secrets &amp; variables
            </NavLink>
          </nav>
          <div className="stack" style={{ gap: 24 }}>
            {error ? <ErrorAlert message={error} /> : null}
            <SecretsSections repo={data.repo} />
          </div>
        </div>
      )}
    </div>
  );
}

function SecretsSections({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data, loading, error, reload } = useLoad(async () => {
    const [secrets, variables, orgSecrets] = await Promise.all([
      fetchRepoSecrets(owner, name),
      fetchRepoVariables(owner, name),
      fetchOrgSecrets(owner),
    ]);
    return { secrets, variables, orgSecrets };
  });

  const [writing, setWriting] = useState(false);
  const [writingVars, setWritingVars] = useState(false);
  const [draftName, setDraftName] = useState('');

  if (loading && data === null) return <p className="muted">Loading secrets…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Secrets are not available.'} />;

  return (
    <>
      <section>
        <div className="sec-h">
          <h2>Repository secrets</h2>
          <span className="sp" />
          <Button variant="primary" size="sm" onClick={() => { setDraftName(''); setWriting((w) => !w); }}>
            <Plus size={14} aria-hidden="true" />
            New secret
          </Button>
        </div>
        {writing ? (
          <EntryForm kind="secret" key={`s-${draftName}`} initial={draftName} owner={owner} repo={name} onSaved={() => void reload()} onCancel={() => setWriting(false)} />
        ) : null}
        {data.secrets.items.length === 0 ? (
          <EmptyState title="No secrets yet" description="Add one with the button above. Values are never shown again." />
        ) : (
          <div className="list">
            {data.secrets.items.map((s) => (
              <div key={s.name} className="list-row">
                <Lock size={16} className="muted" aria-hidden="true" />
                <span className="mono grow">{s.name}</span>
                <span className="small muted">updated {timeAgo(s.updatedAt)}</span>
                <Button size="sm" onClick={() => { setDraftName(s.name); setWriting(true); }}>
                  Update
                </Button>
                {/* DELETE dei segreti non nel contratto OpenAPI: il valore si puo'
                    solo sostituire, per questo il pulsante resta spento. */}
                <Button size="sm" variant="ghost" disabled title="The API has no delete for secrets: values can only be replaced">
                  Delete
                </Button>
              </div>
            ))}
          </div>
        )}
        <p className="hint" style={{ marginTop: 8 }}>
          Values can't be read back. They are masked as <span className="mono">***</span> in logs.
        </p>
      </section>

      <section>
        <div className="sec-h">
          <h2>Organization secrets</h2>
          <span className="badge">inherited · managed by {owner} owners</span>
        </div>
        {data.orgSecrets.items.length === 0 ? (
          <EmptyState title="No organization secrets" description="Nothing is inherited from this organization." />
        ) : (
          <div className="list">
            {data.orgSecrets.items.map((s) => (
              <div key={s.name} className="list-row">
                <Lock size={16} className="muted" aria-hidden="true" />
                <span className="mono grow">{s.name}</span>
                <span className="small muted">available to all {owner} repositories</span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section>
        <div className="sec-h">
          <h2>Variables</h2>
          <span className="sp" />
          <Button size="sm" onClick={() => setWritingVars((w) => !w)}>
            <Plus size={14} aria-hidden="true" />
            New variable
          </Button>
        </div>
        {writingVars ? (
          <EntryForm kind="variable" owner={owner} repo={name} onSaved={() => void reload()} onCancel={() => setWritingVars(false)} />
        ) : null}
        {data.variables.items.length === 0 ? (
          <EmptyState title="No variables yet" description="Variables are readable: they are not secrets." />
        ) : (
          <div className="list">
            {data.variables.items.map((v) => (
              <div key={v.name} className="list-row">
                <span className="mono w-200">{v.name}</span>
                <span className="mono muted grow">{v.value}</span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section>
        <div className="sec-h">
          <h2>Job token default permissions</h2>
        </div>
        <div className="card card-b stack" style={{ gap: 10, fontSize: 13 }}>
          <div className="row">
            <b>Read code, write statuses and packages</b>
            <span className="muted">(default)</span>
          </div>
          <p className="hint">
            A workflow can only ask for less with <span className="mono">permissions:</span>. Changing the default needs an
            API operation that the contract does not have yet, so this section is read-only.
          </p>
          <div className="alert alert-info">
            <ShieldCheck size={16} aria-hidden="true" />
            <div>
              Whatever a workflow asks with <span className="mono">permissions:</span>, the job token never gets more than
              the person or agent who started the run. Pushes made with the job token don't start other workflows.
            </div>
          </div>
        </div>
      </section>
    </>
  );
}

// Form di scrittura: il valore entra nel corpo della PUT e non torna mai nella
// risposta, quindi dopo il salvataggio la lista mostra solo nome e data.
function EntryForm({
  kind,
  owner,
  repo,
  initial = '',
  onSaved,
  onCancel,
}: {
  kind: 'secret' | 'variable';
  owner: string;
  repo: string;
  initial?: string;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { toast } = useToast();

  async function submit(event: FormEvent) {
    event.preventDefault();
    const key = name.trim().toUpperCase();
    if (!KEY_PATTERN.test(key)) {
      setError('Name must be uppercase letters, digits and underscores.');
      return;
    }
    if (!value) {
      setError(kind === 'secret' ? 'Give the secret a value.' : 'Give the variable a value.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (kind === 'secret') await putRepoSecret(owner, repo, { name: key, value });
      else await putRepoVariable(owner, repo, { name: key, value });
      toast(`${key} saved. Values are never shown again.`, 'success');
      setValue('');
      onSaved();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="stack" style={{ gap: 12, marginBottom: 16 }} onSubmit={(e) => void submit(e)}>
      <FormField label={kind === 'secret' ? 'Secret name' : 'Variable name'} hint="Uppercase letters, digits and underscores.">
        <TextInput className="mono" value={name} onChange={(e) => setName(e.target.value)} />
      </FormField>
      <FormField
        label="Value"
        error={error ?? undefined}
        hint={kind === 'secret' ? 'Stored encrypted: the UI never reads it back.' : undefined}
      >
        {kind === 'secret' ? (
          <PasswordInput value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
        ) : (
          <TextInput value={value} onChange={(e) => setValue(e.target.value)} />
        )}
      </FormField>
      <div className="row">
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          Save
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
