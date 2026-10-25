import { Search } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button, EmptyState, ErrorAlert, Select, TextInput } from '../../../components';
import { fetchAppLogs } from '../../../lib/appsApi';
import type { App, AppLogLine } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { logLineClass } from '../../../lib/actionsView';
import { timeAgo } from '../../../lib/format';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { AppHeader } from './AppHeader';

export function AppLogsPage({ repo, app, perms }: { repo: Repository; app: App; perms: AppPermissions }) {
  const [copy, setCopy] = useState('all');
  const [range, setRange] = useState('1h');
  const [search, setSearch] = useState('');
  const [platform, setPlatform] = useState(true);
  const [lines, setLines] = useState<AppLogLine[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    if (!perms.canReadLogs) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const next = await fetchAppLogs(repo.owner.name, repo.name, app.name, {
          follow: range === 'live', since: range === 'live' ? '1h' : range,
          copy: copy === 'all' ? undefined : copy, source: platform ? 'all' : 'app', grep: search || undefined,
        });
        if (!active) return;
        setLines(next);
        setError(null);
      } catch (err) {
        if (active) setError(describeError(err));
      } finally {
        if (active) {
          setLoading(false);
          if (range === 'live') timer = setTimeout(() => void load(), 3000);
        }
      }
    };
    setLoading(true);
    void load();
    return () => { active = false; if (timer) clearTimeout(timer); };
  }, [repo.owner.name, repo.name, app.name, perms.canReadLogs, copy, range, search, platform, refresh]);
  const copies = Array.from({ length: app.copies }, (_, i) => `${app.name}-${i + 1}`);
  return <>
    <AppHeader repo={repo} app={app} mode="app-logs" />
    <main className="page">
      {!perms.canReadLogs ? <p className="muted">Logs require write access to this repository.</p> : <>
        <div className="row" style={{ marginBottom: 12, flexWrap: 'wrap' }}>
          <Select aria-label="App copy" value={copy} onValueChange={setCopy} options={[{ value: 'all', label: 'All copies' }, ...copies.map((c) => ({ value: c, label: c }))]} />
          <Select aria-label="Log time range" value={range} onValueChange={setRange} options={[{ value: 'live', label: 'Live' }, { value: '1h', label: 'Last hour' }, { value: '7d', label: 'Last 7 days' }]} />
          <label className="row" style={{ flex: 1, maxWidth: 320 }}><Search size={16} aria-hidden="true" /><TextInput aria-label="Search logs" placeholder="Search logs" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
          <label className="row small muted"><input type="checkbox" checked={platform} onChange={(e) => setPlatform(e.target.checked)} />Platform events</label>
          {range !== 'live' ? <Button size="sm" onClick={() => setRefresh((n) => n + 1)}>Refresh</Button> : null}
        </div>
        {error ? <ErrorAlert message={error} /> : null}
        <section className="card">
          <div className="card-h small"><span className="dot" style={{ background: range === 'live' ? 'var(--success)' : 'var(--muted)' }} />{range === 'live' ? 'Streaming' : `History · ${range}`} · kept 7 days or 1 GB<span className="sp" /><span className="muted" style={{ fontWeight: 400 }}>Readable with write access</span></div>
          <div className="log" role="log" aria-label="App logs" aria-live={range === 'live' ? 'polite' : 'off'}>
            {loading && lines.length === 0 ? <div>Loading logs…</div> : null}
            {!loading && lines.length === 0 && !error ? <EmptyState title="No logs yet" description="App output and platform events will appear here." /> : null}
            {lines.map((line, i) => <div key={`${line.time}-${line.source}-${i}`} className={logLineClass(line.message) || undefined}><span className="n">{`${new Date(line.time).toLocaleTimeString()} ${line.source === 'platform' ? 'sys' : line.copy ?? app.name}`}</span>{line.message}</div>)}
          </div>
        </section>
      </>}
    </main>
  </>;
}
