import * as Dialog from '@radix-ui/react-dialog';
import { Bot, ChevronDown, Download, File as FileIcon, Folder, GitBranch, History, Search, Shield, Tag as TagIcon, Terminal } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, CopyButton, ErrorAlert, Markdown } from '../../components';
import {
  archiveUrl,
  blobHref,
  decodeContent,
  fetchBranches,
  fetchFilePaths,
  fetchLanguages,
  fetchReadme,
  fetchTags,
  commitsHref,
  fetchTree,
  fuzzyFilter,
  languageColor,
  rawUrl,
  splitRefPath,
  treeHref,
} from '../../lib/codeApi';
import type { Branch, CommitSummary, Tag, Tree, TreeEntry } from '../../lib/codeApi';
import { timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

export interface Refs {
  branches: { items: Branch[]; total: number };
  tags: { items: Tag[]; total: number };
}

/** Pagina Code (mockup 07): `splat` e' la coda `<ref>/<cartella>` della rotta /tree/*. */
export function CodeBrowser({ repo, splat }: { repo: Repository; splat: string }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data: refs, error } = useLoad<Refs>(async () => {
    const [branches, tags] = await Promise.all([fetchBranches(owner, name), fetchTags(owner, name)]);
    return { branches, tags };
  });
  if (error) return <ErrorAlert message={error} />;
  if (!refs) return <p className="muted">Loading code…</p>;

  const defaultRef = repo.defaultBranch || 'main';
  const names = [...refs.branches.items.map((b) => b.name), ...refs.tags.items.map((t) => t.name)];
  const { ref, path } = splat === '' ? { ref: defaultRef, path: '' } : splitRefPath(splat, names);
  // key: cambiando ref o cartella la vista si rimonta e ricarica i dati.
  return <TreeView key={`${ref}\u0000${path}`} repo={repo} refs={refs} refName={ref} path={path} defaultRef={defaultRef} />;
}

function TreeView({ repo, refs, refName, path, defaultRef }: { repo: Repository; refs: Refs; refName: string; path: string; defaultRef: string }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data: tree, error } = useLoad(() => fetchTree(owner, name, refName, path));
  const entries = useMemo(() => (tree ? sortEntries(tree.entries) : []), [tree]);
  const latestTag = useMemo(() => [...refs.tags.items].sort((a, b) => Date.parse(b.taggedAt) - Date.parse(a.taggedAt))[0], [refs.tags.items]);

  return (
    <div className="repo-grid">
      <div className="stack" style={{ gap: 16 }}>
        <div className="row code-bar">
          <RefSwitcher repo={repo} refs={refs} refName={refName} defaultRef={defaultRef} />
          <span className="muted small">
            <GitBranch size={14} aria-hidden="true" /> <b>{refs.branches.total}</b> {refs.branches.total === 1 ? 'branch' : 'branches'} ·{' '}
            <TagIcon size={14} aria-hidden="true" />{' '}
            <Link to={`/${owner}/${name}/tags`}>
              <b>{refs.tags.total}</b> {refs.tags.total === 1 ? 'tag' : 'tags'}
            </Link>
          </span>
          <span className="sp" />
          <Link className="btn" to={commitsHref(owner, name, refName, path)}>
            <History size={16} aria-hidden="true" /> History
          </Link>
          <GoToFile owner={owner} repo={name} refName={refName} />
          <SearchCode owner={owner} repo={name} refName={refName} />
          <CloneMenu repo={repo} refName={refName} />
        </div>

        {path !== '' ? <Breadcrumb owner={owner} repo={name} refName={refName} path={path} defaultRef={defaultRef} /> : null}

        {error ? (
          <ErrorAlert message={error} />
        ) : !tree ? (
          <p className="muted">Loading files…</p>
        ) : (
          <div className="list ftable" aria-label="Files">
            <LastCommitRow commit={headCommit(tree, refs, refName, path)} />
            {path !== '' ? (
              <div className="list-row">
                <span className="fname">
                  <Folder size={16} className="dir" aria-hidden="true" />
                  <Link to={treeHref(owner, name, refName, parentPath(path), defaultRef)}>..</Link>
                </span>
              </div>
            ) : null}
            {entries.map((e) => (
              <EntryRow key={e.path} entry={e} owner={owner} repo={name} refName={refName} defaultRef={defaultRef} />
            ))}
            {tree.truncated ? <div className="list-row muted small">Only the first 1,000 entries are shown.</div> : null}
          </div>
        )}

        {tree ? <ReadmeCard owner={owner} repo={name} refName={refName} path={path} /> : null}
      </div>

      <aside className="about stack" style={{ gap: 18 }} aria-label="About">
        <div>
          <h3>About</h3>
          <p className="muted">{repo.description || 'No description provided.'}</p>
          {tree?.entries.some((e) => e.type === 'file' && /^(license|licence|copying)(\.|$)/i.test(e.name)) && path === '' ? (
            <div className="row">
              <Shield size={16} aria-hidden="true" />
              License file
            </div>
          ) : null}
        </div>
        <Languages key={refName} owner={owner} repo={name} refName={refName} />
        {latestTag ? (
          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
            <h3>Latest tag</h3>
            <div className="row">
              <TagIcon size={16} aria-hidden="true" style={{ color: 'var(--success)' }} />
              <Link className="mono" to={treeHref(owner, name, latestTag.name, '', defaultRef)}>
                {latestTag.name}
              </Link>
              <span className="small subtle">{timeAgo(latestTag.taggedAt)}</span>
            </div>
          </div>
        ) : null}
      </aside>
    </div>
  );
}

function sortEntries(entries: TreeEntry[]): TreeEntry[] {
  const rank = (e: TreeEntry) => (e.type === 'dir' ? 0 : 1);
  return [...entries].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

function parentPath(path: string): string {
  const i = path.lastIndexOf('/');
  return i < 0 ? '' : path.slice(0, i);
}

// L'ultimo commit della riga in alto: alla radice quello del branch/tag, in una
// cartella il piu' recente fra le sue voci.
function headCommit(tree: Tree, refs: Refs, refName: string, path: string): CommitSummary | undefined {
  if (path === '') {
    const known = refs.branches.items.find((b) => b.name === refName)?.commit ?? refs.tags.items.find((t) => t.name === refName)?.commit;
    if (known) return known;
  }
  return [...tree.entries.map((e) => e.lastCommit)].sort((a, b) => Date.parse(b.committer.date) - Date.parse(a.committer.date))[0];
}

function LastCommitRow({ commit }: { commit?: CommitSummary }) {
  if (!commit) return null;
  const agent = commit.author.user?.kind === 'agent';
  return (
    <div className="lastcommit" aria-label="Latest commit">
      <span className={`avatar${agent ? ' av-bot' : ''}`} aria-hidden="true">
        {agent ? <Bot size={14} /> : commit.author.name.slice(0, 1).toUpperCase()}
      </span>
      <b>{commit.author.user?.username ?? commit.author.name}</b>
      {agent ? <span className="badge badge-agent">agent</span> : null}
      <span className="msg">{commit.subject}</span>
      <span className="mono small subtle">{commit.sha.slice(0, 7)}</span>
      <span className="small subtle nowrap">{timeAgo(commit.committer.date)}</span>
    </div>
  );
}

function EntryRow({ entry, owner, repo, refName, defaultRef }: { entry: TreeEntry; owner: string; repo: string; refName: string; defaultRef: string }) {
  const isDir = entry.type === 'dir';
  const to = isDir ? treeHref(owner, repo, refName, entry.path, defaultRef) : blobHref(owner, repo, refName, entry.path);
  return (
    <div className="list-row hover">
      <span className="fname">
        {isDir ? <Folder size={16} className="dir" aria-hidden="true" /> : <FileIcon size={16} aria-hidden="true" />}
        {entry.type === 'submodule' ? <span>{entry.name}</span> : <Link to={to}>{entry.name}</Link>}
      </span>
      <span className="fmsg">{entry.lastCommit.subject}</span>
      <span className="fwhen">{timeAgo(entry.lastCommit.committer.date)}</span>
    </div>
  );
}

function Breadcrumb({ owner, repo, refName, path, defaultRef }: { owner: string; repo: string; refName: string; path: string; defaultRef: string }) {
  const parts = path.split('/');
  return (
    <nav className="crumbs" aria-label="Breadcrumb">
      <Link to={treeHref(owner, repo, refName, '', defaultRef)}>{repo}</Link>
      {parts.map((part, i) => {
        const sub = parts.slice(0, i + 1).join('/');
        return (
          <span key={sub} className="row" style={{ gap: 6 }}>
            <span className="subtle">/</span>
            {i === parts.length - 1 ? <b aria-current="page">{part}</b> : <Link to={treeHref(owner, repo, refName, sub, defaultRef)}>{part}</Link>}
          </span>
        );
      })}
    </nav>
  );
}

function ReadmeCard({ owner, repo, refName, path }: { owner: string; repo: string; refName: string; path: string }) {
  const { data, loading } = useLoad(() => fetchReadme(owner, repo, refName, path).catch(() => null));
  if (loading || !data) return null; // README assente: nessuna scheda
  const source = decodeContent(data);
  if (source === null) return null;
  return (
    <section className="card" aria-label="README">
      <div className="card-h">
        <FileIcon size={16} aria-hidden="true" />
        {data.name}
      </div>
      <div className="readme">
        <Markdown
          source={source}
          basePath={parentPath(data.path)}
          repo={{ owner, name: repo }}
          resolveLink={(p) => blobHref(owner, repo, refName, p)}
          resolveImage={(p) => rawUrl(owner, repo, refName, p)}
        />
      </div>
    </section>
  );
}

function Languages({ owner, repo, refName }: { owner: string; repo: string; refName: string }) {
  const { data } = useLoad(() => fetchLanguages(owner, repo, refName).catch(() => null));
  if (!data || data.languages.length === 0) return null;
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
      <h3>Languages</h3>
      <div className="lang-bar" aria-hidden="true">
        {data.languages.map((l) => (
          <i key={l.name} style={{ width: `${l.percent}%`, background: languageColor(l.name) }} />
        ))}
      </div>
      <ul className="list-clean row small" style={{ flexWrap: 'wrap', gap: 12 }}>
        {data.languages.map((l) => (
          <li key={l.name} className="row" style={{ gap: 5 }}>
            <span className="dot" style={{ background: languageColor(l.name) }} />
            <b>{l.name}</b> {Math.round(l.percent)}%
          </li>
        ))}
      </ul>
    </div>
  );
}

// Selettore di branch e tag con ricerca (R4: parte dal branch principale).
export function RefSwitcher({ repo, refs, refName, defaultRef, hrefFor }: { repo: Repository; refs: Refs; refName: string; defaultRef: string; hrefFor?: (name: string) => string }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const [kind, setKind] = useState<'branches' | 'tags'>(refs.tags.items.some((t) => t.name === refName) && !refs.branches.items.some((b) => b.name === refName) ? 'tags' : 'branches');
  const owner = repo.owner.name;
  const names = (kind === 'branches' ? refs.branches.items.map((b) => b.name) : refs.tags.items.map((t) => t.name)).filter((n) => n.toLowerCase().includes(q.trim().toLowerCase()));
  const isTag = refs.tags.items.some((t) => t.name === refName) && !refs.branches.items.some((b) => b.name === refName);
  return (
    <div style={{ position: 'relative' }}>
      <button type="button" className="btn" aria-haspopup="dialog" aria-expanded={open} aria-label={`Switch branch or tag, current: ${refName}`} onClick={() => setOpen((o) => !o)}>
        {isTag ? <TagIcon size={16} aria-hidden="true" /> : <GitBranch size={16} aria-hidden="true" />}
        <span className="mono">{refName}</span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {open ? (
        <div className="menu pop" role="dialog" aria-label="Switch branch or tag" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
          <input className="input" aria-label="Find a branch or tag" placeholder="Find a branch or tag" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
          <div className="seg" role="group" aria-label="Kind" style={{ margin: '8px 0' }}>
            <button type="button" className={kind === 'branches' ? 'active' : undefined} aria-pressed={kind === 'branches'} onClick={() => setKind('branches')}>
              Branches
            </button>
            <button type="button" className={kind === 'tags' ? 'active' : undefined} aria-pressed={kind === 'tags'} onClick={() => setKind('tags')}>
              Tags
            </button>
          </div>
          {names.length === 0 ? <p className="muted small">Nothing found.</p> : null}
          {names.map((n) => (
            <Link key={n} className="pop-item" to={hrefFor ? hrefFor(n) : treeHref(owner, repo.name, n, '', defaultRef)} onClick={() => setOpen(false)}>
              <span className="mono">{n}</span>
              {n === defaultRef && kind === 'branches' ? <span className="badge">default</span> : null}
            </Link>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function CloneMenu({ repo, refName }: { repo: Repository; refName: string }) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<'https' | 'ssh' | 'cli'>('https');
  const { owner, name } = { owner: repo.owner.name, name: repo.name };
  const sshAvailable = Boolean(repo.cloneUrls.ssh);
  const sshUrl = repo.cloneUrls.sshShort ?? repo.cloneUrls.ssh ?? '';
  const sshPort = repo.cloneUrls.ssh ? sshPortOf(repo.cloneUrls.ssh, repo.cloneUrls.sshShort) : '';
  const methods = (sshAvailable ? ['https', 'ssh', 'cli'] : ['https', 'cli']) as readonly ('https' | 'ssh' | 'cli')[];
  const activeTab = tab === 'ssh' && !sshAvailable ? 'https' : tab;
  const value = activeTab === 'https' ? repo.cloneUrls.https : activeTab === 'ssh' ? sshUrl : `gs repo clone ${repo.fullName}`;
  return (
    <div style={{ position: 'relative' }}>
      <Button variant="primary" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Terminal size={16} aria-hidden="true" />
        Clone
        <ChevronDown size={16} aria-hidden="true" />
      </Button>
      {open ? (
        <div className="menu pop pop-right" role="dialog" aria-label="Clone" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
          <div className="row" style={{ marginBottom: 10 }}>
            <b>Clone</b>
            <span className="sp" />
            <span className="seg" role="group" aria-label="Clone method">
              {methods.map((t) => (
                <button key={t} type="button" className={activeTab === t ? 'active' : undefined} aria-pressed={activeTab === t} onClick={() => setTab(t)}>
                  {t === 'cli' ? 'CLI' : t.toUpperCase()}
                </button>
              ))}
            </span>
          </div>
          {activeTab === 'ssh' ? <div className="small muted">SSH (port {sshPort})</div> : null}
          <div className="row" style={{ flexWrap: 'nowrap', marginTop: 4 }}>
            <input className="input mono" aria-label="Clone URL" value={value} readOnly />
            <CopyButton value={value} />
          </div>
          {activeTab === 'https' ? (
            <p className="hint small muted" style={{ marginTop: 8 }}>
              Use an <Link to="/settings/tokens">access token</Link> as password.
            </p>
          ) : null}
          <div style={{ height: 1, background: 'var(--border)', margin: '10px -12px' }} />
          <a className="row small" style={{ color: 'var(--text)' }} href={archiveUrl(owner, name, refName, 'zip')} download>
            <Download size={14} aria-hidden="true" />
            Download ZIP
          </a>
          <a className="row small" style={{ color: 'var(--text)', marginTop: 6 }} href={archiveUrl(owner, name, refName, 'tar.gz')} download>
            <Download size={14} aria-hidden="true" />
            Download tar.gz
          </a>
        </div>
      ) : null}
    </div>
  );
}

// La porta SSH viene dall'indirizzo dell'API; la forma corta (scp) c'e' solo con la 22 (R7).
function sshPortOf(ssh: string, short?: string): string {
  if (short) return '22';
  try {
    return new URL(ssh).port || '22';
  } catch {
    return '22';
  }
}

// B5 · Go to file: corrispondenza approssimata sull'elenco dei percorsi.
function GoToFile({ owner, repo, refName }: { owner: string; repo: string; refName: string }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const [paths, setPaths] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [truncated, setTruncated] = useState(false);
  useEffect(() => {
    if (!open || paths !== null) return;
    let live = true;
    fetchFilePaths(owner, repo, refName).then(
      (l) => {
        if (!live) return;
        setPaths(l.paths);
        setTruncated(l.truncated);
      },
      (err: unknown) => live && setError(describeError(err)),
    );
    return () => {
      live = false;
    };
  }, [open, paths, owner, repo, refName]);
  const found = useMemo(() => (paths ? fuzzyFilter(q, paths) : []), [q, paths]);
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <Button>Go to file</Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="dialog dialog-wide">
          <Dialog.Title className="dialog-title">Go to file</Dialog.Title>
          <Dialog.Description className="sr-only">Type part of a file path; letters can be spread across the name.</Dialog.Description>
          <input className="input" aria-label="File path" placeholder="Type a file name" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
          {error ? <ErrorAlert message={error} /> : null}
          {!error && paths === null ? <p className="muted small">Loading files…</p> : null}
          {truncated ? <p className="muted small">The list is limited to the first 50,000 files.</p> : null}
          {paths !== null && found.length === 0 ? <p className="muted small">No matching files.</p> : null}
          <ul className="goto-list" aria-label="Matching files">
            {found.map((p) => (
              <li key={p}>
                <Link className="pop-item mono" to={blobHref(owner, repo, refName, p)} onClick={() => setOpen(false)}>
                  <FileIcon size={14} aria-hidden="true" />
                  {p}
                </Link>
              </li>
            ))}
          </ul>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

// B5 · Search code: porta ai risultati (pagina di GIT-94) con la query e il ref.
function SearchCode({ owner, repo, refName }: { owner: string; repo: string; refName: string }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const navigate = useNavigate();
  function submit(e: FormEvent) {
    e.preventDefault();
    const query = q.trim();
    if (query === '') return;
    setOpen(false);
    navigate(`/${owner}/${repo}/search?q=${encodeURIComponent(query)}&ref=${encodeURIComponent(refName)}`);
  }
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <Button>
          <Search size={16} aria-hidden="true" />
          Search code
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="dialog">
          <Dialog.Title className="dialog-title">Search code</Dialog.Title>
          <Dialog.Description className="dialog-desc">
            Text search in {owner}/{repo} on <span className="mono">{refName}</span>.
          </Dialog.Description>
          <form onSubmit={submit} className="stack">
            <input className="input" aria-label="Search query" placeholder="Search for text" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
            <div className="dialog-actions">
              <Button type="submit" variant="primary">
                Search
              </Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
