import { Bot, Download, Key, Terminal } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { CopyButton, ErrorAlert, Logo } from '../components';
import { fetchDownloadsIndex } from '../lib/downloadsApi';
import type { DownloadBinary } from '../lib/downloadsApi';
import { useLoad } from '../lib/useLoad';

const OS_LABEL: Record<DownloadBinary['os'], string> = { linux: 'Linux', darwin: 'macOS', windows: 'Windows' };
const OS_ORDER: DownloadBinary['os'][] = ['linux', 'darwin', 'windows'];

function archLabel(b: DownloadBinary): string {
  if (b.os === 'darwin') return b.arch === 'amd64' ? 'Intel' : 'Apple silicon';
  return b.arch;
}

function formatSize(bytes: number): string {
  if (!bytes) return '';
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

// Pagina pubblica /downloads (mockup "downloads"): binari gs per piattaforma
// con checksum, comandi d'installazione con l'host corrente, skills e
// certificato della CA. Sta fuori da RequireAuth e da AppShell (sidebar e
// topbar chiamano API che richiedono la sessione).
export function DownloadsPage() {
  const { data, loading, error } = useLoad(fetchDownloadsIndex);
  const [tab, setTab] = useState<'unix' | 'windows'>('unix');
  const origin = window.location.origin;
  const host = window.location.host;
  const shCmd = `curl -fsSL ${origin}${data?.install.sh ?? '/install-gs.sh'} | sh`;
  const psCmd = `irm ${origin}${data?.install.ps1 ?? '/install-gs.ps1'} | iex`;
  const cmd = tab === 'unix' ? shCmd : psCmd;

  const groups = OS_ORDER.map((os) => ({ os, items: (data?.binaries ?? []).filter((b) => b.os === os) })).filter(
    (g) => g.items.length > 0,
  );

  return (
    <div className="public-page">
      <header className="topbar">
        <div className="crumb row">
          <Logo size={18} />
          <b>GitStack</b>
        </div>
        <span className="sp" />
        <Link className="btn btn-ghost btn-sm" to="/">
          Sign in
        </Link>
      </header>
      <main className="page downloads-page">
        <div className="page-h">
          <h1>CLI &amp; skills</h1>
          {data ? <span className="badge badge-accent">GitStack {data.version}</span> : null}
        </div>
        <p className="muted section-lead">
          Download <span className="mono">gs</span> and the agent skills from this GitStack. They always match the server
          version, and work without internet access.
        </p>

        {error ? (
          <ErrorAlert message={`${error} The downloads are not published on this instance yet.`} />
        ) : null}
        {loading && !data ? <p className="muted">Loading downloads…</p> : null}

        <div className="card section-gap">
          <div className="card-h">
            <Terminal size={16} aria-hidden="true" />
            Install in one command
            <span className="sp" />
            <span className="seg" role="group" aria-label="Platform">
              <button type="button" className={tab === 'unix' ? 'active' : ''} aria-pressed={tab === 'unix'} onClick={() => setTab('unix')}>
                Linux &amp; macOS
              </button>
              <button type="button" className={tab === 'windows' ? 'active' : ''} aria-pressed={tab === 'windows'} onClick={() => setTab('windows')}>
                Windows
              </button>
            </span>
          </div>
          <div className="card-b">
            <div className="row nowrap">
              <div className="cmdblock grow" aria-label="Install command">
                <span className="p">{tab === 'unix' ? '$ ' : 'PS> '}</span>
                {cmd}
              </div>
              <CopyButton value={cmd} />
            </div>
            {data?.ca_cert ? (
              <p className="small muted section-gap">
                This instance uses its own certificate authority:{' '}
                <a href={data.ca_cert} download>
                  download the CA certificate
                </a>{' '}
                and trust it before installing.
              </p>
            ) : null}
          </div>
        </div>

        {data ? (
          <div className="card section-gap">
            <div className="card-h">
              <Download size={16} aria-hidden="true" />
              gs {data.version} binaries
            </div>
            <ul className="list" aria-label="Binaries">
              {groups.map((g) => (
                <li key={g.os} className="list-row">
                  <b className="os-name">{OS_LABEL[g.os]}</b>
                  <div className="grow stack">
                    {g.items.map((b) => (
                      <div key={b.file} className="row nowrap">
                        <a className="btn btn-sm" href={b.url} download aria-label={`${archLabel(b)} ${b.file}`}>
                          <Download size={14} aria-hidden="true" />
                          {archLabel(b)}
                        </a>
                        <span className="small muted">{b.file}{b.size ? ` · ${formatSize(b.size)}` : ''}</span>
                        <span className="sp" />
                        <span className="mono small muted" title={b.sha256}>
                          sha256 {b.sha256.slice(0, 12)}…
                        </span>
                        <CopyButton value={b.sha256} label="Copy sha256" />
                      </div>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
            <div className="card-b small muted">
              Checksums:{' '}
              <a className="mono" href={data.checksums}>
                SHA256SUMS
              </a>{' '}
              · <span className="mono">gs</span> warns when it talks to a different GitStack version and refuses only a
              different major version.
            </div>
          </div>
        ) : null}

        <div className="repo-grid downloads-grid">
          <div className="card">
            <div className="card-h">
              <Key size={16} aria-hidden="true" />
              Sign in
            </div>
            <div className="card-b stack">
              <p className="small muted">People: create a token in the browser and paste it.</p>
              <div className="cmdblock">
                <span className="p">$ </span>gs auth login --host {host} --web{'\n'}
                <span className="p">$ </span>gs auth setup-git
              </div>
              <p className="small muted">Agents and CI: set environment variables, nothing interactive.</p>
              <div className="cmdblock">
                GS_HOST={host}
                {'\n'}GS_TOKEN=gst_… <span className="tk-c"># token of the agent account</span>
              </div>
            </div>
          </div>
          <div className="card">
            <div className="card-h">
              <Bot size={16} aria-hidden="true" />
              Agent skills
            </div>
            <div className="card-b stack">
              <p className="small muted">
                Standard Agent Skills (<span className="mono">SKILL.md</span>) for coding agents. <span className="mono">gs</span>{' '}
                detects the agent and installs them in the project.
              </p>
              <div className="cmdblock">
                <span className="p">$ </span>gs skills install{'\n'}
                <span className="p">$ </span>gs skills install --agents-md <span className="tk-c"># agents without skills</span>
              </div>
              {data?.skills ? (
                <>
                  <a className="btn btn-sm" href={data.skills.url} download>
                    <Download size={14} aria-hidden="true" />
                    Download skills (.zip)
                  </a>
                  <span className="mono small muted" title={data.skills.sha256}>
                    sha256 {data.skills.sha256.slice(0, 12)}…
                  </span>
                </>
              ) : null}
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}
