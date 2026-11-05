import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Button, ErrorAlert } from '../components';
import { fetchSession } from '../lib/authApi';
import * as api from '../lib/databasesApi';
import type { DatabaseCredentials } from '../lib/databasesApi';
import { ApiError, describeError } from '../lib/http';
import { fetchOrganizations } from '../lib/orgsApi';
import { useLoad } from '../lib/useLoad';

const dbPath = (owner: string, name: string) => `/databases/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`;
const disabledText = 'Managed databases are turned off. Ask an administrator to enable them.';
const dbError = (error: unknown) => error instanceof ApiError && error.code === 'databases_disabled' ? disabledText : describeError(error);

async function owners() {
  const [session, orgs] = await Promise.all([fetchSession(), fetchOrganizations()]);
  return [session.user.username, ...orgs.map((org) => org.name)];
}

async function availableOwners() {
  const names = await owners();
  const results = await Promise.all(names.map(async (name) => {
    try { return { name, list: await api.fetchDatabases(name), error: null }; }
    catch (error) {
      if (error instanceof ApiError && error.status === 404) return null;
      return { name, list: null, error: dbError(error) };
    }
  }));
  return results.filter((result) => result !== null);
}

export function DatabasesPage() {
  const { data, loading, error } = useLoad(availableOwners);
  return <div><div className="page-h"><h1>Databases</h1><span className="sp" /><Link className="btn btn-primary" to="/databases/new">New database</Link></div>
    {loading && !data ? <p>Loading databases…</p> : null}
    {error ? <ErrorAlert message={error} /> : null}
    {data?.map((group) => <section className="card section-gap" key={group.name}><div className="card-h">{group.name}</div><div className="card-b">
      {group.error === disabledText ? <p>{disabledText}</p> : group.error ? <ErrorAlert message={group.error} /> : group.list?.items.length ? <ul className="list list-clean">{group.list.items.map((db) => <li className="list-row" key={db.id}><Link to={dbPath(db.owner, db.name)}>{db.name}</Link><span className="sp" /><span>{db.status} · PostgreSQL {db.pgMajor}</span></li>)}</ul> : <p className="muted">No databases yet.</p>}
    </div></section>)}
  </div>;
}

function Credentials({ credentials }: { credentials: DatabaseCredentials }) {
  const [shown, setShown] = useState(false);
  return <div className="alert"><p>Save these credentials now; the password cannot be read again.</p><p>User: <code>{credentials.username}</code></p><Button type="button" onClick={() => setShown(!shown)}>{shown ? 'Hide password' : 'Show password'}</Button>{shown ? <p><code>{credentials.password}</code></p> : null}</div>;
}

export function NewDatabasePage() {
  const { data, error } = useLoad(availableOwners);
  const [ownerChoice, setOwner] = useState('');
  const [name, setName] = useState('');
  const [major, setMajor] = useState<16 | 17>(16);
  const [size, setSize] = useState(1);
  const [failure, setFailure] = useState<string | null>(null);
  const [created, setCreated] = useState<Awaited<ReturnType<typeof api.addDatabase>> | null>(null);
  const [busy, setBusy] = useState(false);
  const eligible = data?.filter((group) => group.list) ?? [];
  const owner = ownerChoice || eligible[0]?.name || '';
  async function submit() {
    setBusy(true); setFailure(null);
    try { setCreated(await api.addDatabase(owner, { name, pgMajor: major, sizeBytes: size * 1024 ** 3 })); }
    catch (err) {
      const missing = err instanceof ApiError && err.code === 'insufficient_capacity' ? err.details?.missing : null;
      const parts = missing && typeof missing === 'object' ? Object.entries(missing).map(([key, value]) => `${key}: ${String(value)}`).join(', ') : '';
      setFailure(`${dbError(err)}${parts ? ` Missing capacity: ${parts}` : ''}`);
    } finally { setBusy(false); }
  }
  if (created) return <div><h1>Database created</h1><Credentials credentials={created.credentials} /><Link to={dbPath(created.database.owner, created.database.name)}>Open database</Link></div>;
  return <div><h1>New database</h1>{error ? <ErrorAlert message={error} /> : null}{data?.filter((g) => g.error).map((g) => <ErrorAlert key={g.name} message={g.error ?? ''} />)}
    <form className="card card-b stack" onSubmit={(e) => { e.preventDefault(); void submit(); }}>
      {failure ? <ErrorAlert message={failure} /> : null}
      <label>Owner <select className="input" value={owner} onChange={(e) => setOwner(e.target.value)}>{eligible.map((g) => <option key={g.name}>{g.name}</option>)}</select></label>
      <label>Name <input className="input" required value={name} onChange={(e) => setName(e.target.value)} /></label>
      <label>PostgreSQL version <select className="input" value={major} onChange={(e) => setMajor(Number(e.target.value) as 16 | 17)}><option value={16}>16</option><option value={17}>17</option></select></label>
      <label>Size (GiB) <input className="input" type="number" min="1" required value={size} onChange={(e) => setSize(Number(e.target.value))} /></label>
      <Button type="submit" variant="primary" disabled={!owner || busy}>Create database</Button>
    </form></div>;
}

type Identity = { owner: string; name: string };

function Links({ owner, name }: Identity) {
  const { data, error, reload } = useLoad(() => api.fetchDatabaseLinks(owner, name));
  const [failure, setFailure] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<DatabaseCredentials | null>(null);
  async function unlink(id: string) { try { setFailure(null); await api.removeDatabaseLink(owner, name, id); await reload(); } catch (err) { setFailure(dbError(err)); } }
  async function rotate(id: string) { try { setFailure(null); setCredentials((await api.rotateLink(owner, name, id)).credentials); } catch (err) { setFailure(dbError(err)); } }
  return <section className="card section-gap"><div className="card-h">Linked apps</div><div className="card-b">{error || failure ? <ErrorAlert message={failure ?? error ?? ''} /> : null}
    {credentials ? <Credentials key={credentials.password} credentials={credentials} /> : null}
    {data?.items.length ? <ul>{data.items.map((link) => <li key={link.id}>{link.app.owner}/{link.app.repo}/{link.app.name} · {link.mode} · {link.state} <Button onClick={() => void rotate(link.id)}>Rotate link password</Button> <Button onClick={() => void unlink(link.id)}>Unlink</Button></li>)}</ul> : data ? <p>No linked apps.</p> : null}
  </div></section>;
}

function Backups({ owner, name }: Identity) {
  const { data, error, reload } = useLoad(() => api.fetchDatabaseBackups(owner, name));
  const [failure, setFailure] = useState<string | null>(null);
  const [confirm, setConfirm] = useState('');
  async function run(action: () => Promise<unknown>) { try { setFailure(null); await action(); await reload(); } catch (err) { setFailure(dbError(err)); } }
  return <section className="card section-gap"><div className="card-h">Backups</div><div className="card-b stack">{error || failure ? <ErrorAlert message={failure ?? error ?? ''} /> : null}
    <Button onClick={() => void run(() => api.addDatabaseBackup(owner, name))}>Create backup</Button>
    <Button onClick={() => void reload()}>Refresh backups</Button>
    <p>Restoring replaces the database and restarts linked apps. Type {owner}/{name} to confirm.</p><label>Restore confirmation <input className="input" value={confirm} onChange={(e) => setConfirm(e.target.value)} /></label>
    <ul>{data?.items.map((backup) => <li key={backup.id}>{backup.kind} · {backup.status} · {backup.createdAt} {backup.status === 'failed' && backup.error ? <ErrorAlert message={backup.error} /> : null} <Button disabled={backup.status !== 'completed' || confirm !== `${owner}/${name}`} onClick={() => void run(() => api.restoreBackup(owner, name, backup.id, { target: 'same', confirm }))}>Restore</Button></li>)}</ul>
  </div></section>;
}

function Lan({ owner, name }: Identity) {
  const { data, error, reload } = useLoad(() => api.fetchDatabaseLan(owner, name));
  const [failure, setFailure] = useState<string | null>(null);
  async function toggle() { try { setFailure(null); await api.changeDatabaseLan(owner, name, !data?.enabled); await reload(); } catch (err) { setFailure(dbError(err)); await reload(); } }
  return <section className="card section-gap"><div className="card-h">LAN access</div><div className="card-b">{error || failure ? <ErrorAlert message={failure ?? error ?? ''} /> : null}
    {data ? <><p>{data.enabled ? `Enabled · ${data.host}:${data.port}` : 'Disabled'}</p>{data.enabled ? <><p><code>postgresql://{name}_owner:&lt;password&gt;@{data.host}:{data.port}/{name}?sslmode=verify-full</code></p><Link to="/help/ca">CA certificate</Link>{data.ufwCommand ? <p>{data.ufwCommand}</p> : null}</> : null}<Button onClick={() => void toggle()}>{data.enabled ? 'Disable LAN access' : 'Enable LAN access'}</Button></> : null}
  </div></section>;
}

export function DatabaseDetailPage() {
  const { owner = '', name = '' } = useParams();
  return <DatabaseDetail key={`${owner}/${name}`} owner={owner} name={name} />;
}
function DatabaseDetail({ owner, name }: Identity) {
  const { data, error, reload } = useLoad(() => api.fetchDatabase(owner, name));
  const [confirm, setConfirm] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<DatabaseCredentials | null>(null);
  const navigate = useNavigate();
  async function rotate() { try { setFailure(null); setCredentials((await api.rotateCredentials(owner, name)).credentials); } catch (err) { setFailure(dbError(err)); } }
  async function remove() { try { setFailure(null); await api.removeDatabase(owner, name, confirm); void navigate('/databases'); } catch (err) { setFailure(dbError(err)); } }
  return <div><h1>{owner}/{name}</h1>{error ? <ErrorAlert message={error} /> : null}{data ? <><div className="card card-b"><p>Status: {data.status} <Button onClick={() => void reload()}>Refresh</Button></p><p>PostgreSQL {data.pgMajor} · Disk: {data.usage ? `${data.usage.databaseBytes} / ${data.sizeBytes} bytes (${data.usage.diskState})` : 'usage unavailable'}</p><p>Internal address: <code>pg.gitstack-db-{data.id}.svc.cluster.local:5432</code></p></div>
    <Links owner={owner} name={name} /><Backups owner={owner} name={name} /><Lan owner={owner} name={name} />
    <section className="card section-gap"><div className="card-h">Credentials and deletion</div><div className="card-b stack">{failure ? <ErrorAlert message={failure} /> : null}<Button onClick={() => void rotate()}>Rotate password</Button>{credentials ? <Credentials key={credentials.password} credentials={credentials} /> : null}<p>Deleting this database removes its data, backups and links.</p><label>Type {owner}/{name} to delete <input className="input" value={confirm} onChange={(e) => setConfirm(e.target.value)} /></label><Button disabled={confirm !== `${owner}/${name}`} onClick={() => void remove()}>Delete database</Button></div></section>
  </> : null}</div>;
}
