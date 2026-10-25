import { Search } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button, ErrorAlert, LogView, TextInput } from '../../../components';
import { fetchAppLogs } from '../../../lib/appsApi';
import type { App, AppLogLine } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { logLineClass } from '../../../lib/actionsView';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { AppHeader } from './AppHeader';

export function AppLogsPage({ repo, app, perms }: { repo: Repository; app: App; perms: AppPermissions }) {
  const [copy, setCopy] = useState('all');
  const [range, setRange] = useState('live');
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [platform, setPlatform] = useState(true);
  const [lines, setLines] = useState<AppLogLine[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);
  useEffect(() => {
    if (!perms.canReadLogs) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const next = await fetchAppLogs(repo.owner.name, repo.name, app.name, {
          since: range === 'live' ? '1h' : range,
          copy: copy === 'all' ? undefined : copy, source: platform ? 'all' : 'app', grep: debouncedSearch || undefined,
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
  }, [repo.owner.name, repo.name, app.name, perms.canReadLogs, copy, range, debouncedSearch, platform, refresh]);
  const copies = Array.from({ length: app.copies }, (_, i) => `${app.name}-${i + 1}`);
  return <>
    <AppHeader repo={repo} app={app} mode="app-logs" />
    <main className="page">
      {!perms.canReadLogs ? <p className="muted">Logs require write access to this repository.</p> : <>
        <div className="row" style={{ marginBottom: 12, flexWrap: 'wrap' }}>
          <span className="seg" role="group" aria-label="App copy">
            {['all', ...copies].map((value) => <button key={value} type="button" className={copy === value ? 'active' : undefined} aria-pressed={copy === value} onClick={() => setCopy(value)}>{value === 'all' ? 'All copies' : value}</button>)}
          </span>
          <span className="seg" role="group" aria-label="Log time range">
            {(['live', '1h', '7d'] as const).map((value) => <button key={value} type="button" className={range === value ? 'active' : undefined} aria-pressed={range === value} onClick={() => setRange(value)}>{value === 'live' ? 'Live' : value === '1h' ? 'Last hour' : 'Last 7 days'}</button>)}
          </span>
          <label className="row" style={{ flex: 1, maxWidth: 320 }}><Search size={16} aria-hidden="true" /><TextInput aria-label="Search logs" placeholder="Search logs" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
          <div className="row small muted"><button type="button" className={`platform-switch${platform ? ' on' : ''}`} role="switch" aria-label="Platform events" aria-checked={platform} onClick={() => setPlatform((value) => !value)}><span /></button><span>Platform events</span></div>
          {range !== 'live' ? <Button size="sm" onClick={() => setRefresh((n) => n + 1)}>Refresh</Button> : null}
        </div>
        {error ? <ErrorAlert message={error} /> : null}
        <section className="card">
          <div className="card-h small"><span className="dot" style={{ background: range === 'live' ? 'var(--success)' : 'var(--muted)' }} />{range === 'live' ? 'Streaming' : `History · ${range}`} · kept 7 days or 1 GB<span className="sp" /><span className="muted" style={{ fontWeight: 400 }}>Readable with write access</span></div>
          <LogView label="App logs" live={range === 'live'} loadingText={loading ? 'Loading logs…' : undefined} emptyText={!loading && !error ? 'No logs yet. App output and platform events will appear here.' : undefined} lines={lines.map((line, i) => ({ key: `${line.time}-${line.source}-${i}`, prefix: `${new Date(line.time).toLocaleTimeString()} ${line.source === 'platform' ? 'sys' : line.copy ?? app.name}`, text: line.message, cls: logLineClass(line.message) }))} />
        </section>
      </>}
    </main>
  </>;
}
