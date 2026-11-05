import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, ErrorAlert } from '../../../components';
import * as api from '../../../lib/databasesApi';
import { ApiError, describeError } from '../../../lib/http';
import { useLoad } from '../../../lib/useLoad';

export function AppDatabases({ owner, repo, app }: { owner: string; repo: string; app: string }) {
  const { data, error, reload } = useLoad(async () => {
    const databases = await api.fetchDatabases(owner);
    return Promise.all(databases.items.map(async (database) => {
      try { return { database, links: (await api.fetchDatabaseLinks(owner, database.name)).items, error: null }; }
      catch (err) { return { database, links: [], error: describeError(err) }; }
    }));
  });
  const [failure, setFailure] = useState<string | null>(null);
  const [choice, setChoice] = useState('');
  const [mode, setMode] = useState<'rw' | 'ro'>('rw');
  async function run(action: () => Promise<unknown>) { try { setFailure(null); await action(); await reload(); } catch (err) { setFailure(err instanceof ApiError && err.code === 'databases_disabled' ? 'Managed databases are turned off. Ask an administrator to enable them.' : describeError(err)); } }
  return <section className="card section-gap"><div className="card-h">Linked databases</div><div className="card-b stack">
    {error || failure ? <ErrorAlert message={failure ?? error ?? ''} /> : null}
    {data?.map(({ database, links, error: linkError }) => <div key={database.id}>
      {linkError ? <ErrorAlert message={`${database.name}: ${linkError}`} /> : null}
      {links.filter((link) => link.app.owner === owner && link.app.repo === repo && link.app.name === app).map((link) => <p key={link.id}><Link to={`/databases/${encodeURIComponent(owner)}/${encodeURIComponent(database.name)}`}>{database.name}</Link> · {link.mode} · {link.state} <Button onClick={() => void run(() => api.removeDatabaseLink(owner, database.name, link.id))}>Unlink</Button></p>)}
    </div>)}
    {data?.length ? <form className="row" onSubmit={(event) => { event.preventDefault(); void run(() => api.addDatabaseLink(owner, choice, { app: { owner, repo, name: app }, mode })); }}>
      <label>Database <select className="input" required value={choice} onChange={(event) => setChoice(event.target.value)}><option value="">Choose a database</option>{data.map(({ database }) => <option key={database.id} value={database.name}>{database.name}</option>)}</select></label>
      <label>Access <select className="input" value={mode} onChange={(event) => setMode(event.target.value as 'rw' | 'ro')}><option value="rw">Read and write</option><option value="ro">Read only</option></select></label>
      <Button type="submit" disabled={!choice}>Link database</Button>
    </form> : null}
  </div></section>;
}
