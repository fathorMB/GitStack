import { Download, Info, Tag as TagIcon } from 'lucide-react';
import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Checkbox, DiffView, ErrorAlert } from '../../components';
import type { DiffViewMode } from '../../components';
import { blobHref, commitDownloadUrl, commitHref, fetchCommit } from '../../lib/codeApi';
import type { CommitDetail, FileDiff } from '../../lib/codeApi';
import { messageBody } from '../../lib/commitView';
import { timeAgo } from '../../lib/format';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';
import { CommitAuthor } from './CommitsPage';

/** Pagina di dettaglio di un commit (mockup 10): `sha` dalla rotta /commit/:sha. */
export function CommitPage({ repo }: { repo: Repository }) {
  const { sha = '' } = useParams();
  const [ignoreWs, setIgnoreWs] = useState(false);
  const [mode, setMode] = useState<DiffViewMode>('unified');
  return <CommitLoader key={`${sha}:${ignoreWs}`} repo={repo} sha={sha} ignoreWs={ignoreWs} onIgnoreWs={setIgnoreWs} mode={mode} onMode={setMode} />;
}

interface ViewState {
  ignoreWs: boolean;
  onIgnoreWs: (v: boolean) => void;
  mode: DiffViewMode;
  onMode: (m: DiffViewMode) => void;
}

function CommitLoader({ repo, sha, ...view }: { repo: Repository; sha: string } & ViewState) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data, error } = useLoad(() => fetchCommit(owner, name, sha, view.ignoreWs));
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted">Loading commit…</p>;
  return <CommitDetailView owner={owner} repo={name} detail={data} {...view} />;
}

function CommitDetailView({ owner, repo, detail, ignoreWs, onIgnoreWs, mode, onMode }: { owner: string; repo: string; detail: CommitDetail } & ViewState) {
  const c = detail.commit;
  const body = messageBody(c);
  const sameCommitter = c.committer.email === c.author.email && c.committer.name === c.author.name;
  const fileHref = (f: FileDiff) => blobHref(owner, repo, c.sha, f.path);
  const diffUrl = commitDownloadUrl(owner, repo, c.sha, 'diff', ignoreWs);
  const patchUrl = commitDownloadUrl(owner, repo, c.sha, 'patch', ignoreWs);

  return (
    <div className="page" style={{ maxWidth: 1100 }}>
      <div className="card" style={{ marginBottom: 18 }}>
        <div className="card-b">
          <h1 style={{ fontSize: 20, fontWeight: 600 }}>{c.subject}</h1>
          {body ? (
            <p className="muted" style={{ marginTop: 8, whiteSpace: 'pre-wrap' }} aria-label="Commit message">
              {body}
            </p>
          ) : null}
          {detail.tags && detail.tags.length > 0 ? (
            <div className="row" style={{ marginTop: 10 }} aria-label="Tags">
              {detail.tags.map((t) => (
                <span key={t} className="badge">
                  <TagIcon size={12} style={{ color: 'var(--success)' }} aria-hidden="true" />
                  {t}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        <div className="lastcommit" style={{ borderTop: '1px solid var(--border)', borderRadius: '0 0 var(--r-lg) var(--r-lg)', flexWrap: 'wrap' }}>
          <CommitAuthor commit={c} />
          <span className="muted">authored {timeAgo(c.author.date)}</span>
          {!sameCommitter ? (
            <span className="muted" aria-label="Committer">
              · committed by <b style={{ color: 'var(--text)' }}>{c.committer.user?.username ?? c.committer.name}</b> {timeAgo(c.committer.date)}
            </span>
          ) : null}
          <span className="sp" />
          <span className="muted small" aria-label="Parents">
            {c.parents.length === 0 ? 'root commit' : c.parents.length === 1 ? 'parent' : 'parents'}{' '}
            {c.parents.map((p) => (
              <Link key={p} className="mono" to={commitHref(owner, repo, p)} style={{ marginRight: 6 }}>
                {p.slice(0, 7)}
              </Link>
            ))}
          </span>
          <span className="small muted">
            commit <span className="mono" title={c.sha}>{c.sha.slice(0, 7)}</span>
          </span>
        </div>
      </div>

      <div className="row" style={{ marginBottom: 12 }}>
        <span className="muted">
          Showing <b style={{ color: 'var(--text)' }}>{detail.filesChanged} changed {detail.filesChanged === 1 ? 'file' : 'files'}</b> with{' '}
          <b style={{ color: 'var(--success)' }}>{detail.additions} {detail.additions === 1 ? 'addition' : 'additions'}</b> and{' '}
          <b style={{ color: 'var(--danger)' }}>{detail.deletions} {detail.deletions === 1 ? 'deletion' : 'deletions'}</b>
        </span>
        <span className="sp" />
        <Checkbox label="Ignore whitespace" checked={ignoreWs} onCheckedChange={(v) => onIgnoreWs(v === true)} />
        {!detail.listOnly ? (
          <span className="seg" role="group" aria-label="Diff view">
            <button type="button" className={mode === 'unified' ? 'active' : undefined} aria-pressed={mode === 'unified'} onClick={() => onMode('unified')}>
              Unified
            </button>
            <button type="button" className={mode === 'split' ? 'active' : undefined} aria-pressed={mode === 'split'} onClick={() => onMode('split')}>
              Split
            </button>
          </span>
        ) : null}
        <a className="btn btn-sm" href={diffUrl} download>
          <Download size={14} aria-hidden="true" />
          .diff
        </a>
        <a className="btn btn-sm" href={patchUrl} download>
          <Download size={14} aria-hidden="true" />
          .patch
        </a>
      </div>

      {detail.listOnly ? (
        <div className="alert alert-info" role="status" style={{ marginBottom: 16 }}>
          <Info size={16} aria-hidden="true" />
          <div>
            This commit is too large to show inline (over 300 files or 20,000 changed lines): only the list of files is shown
            {detail.filesChanged > detail.files.length ? `, the first ${detail.files.length} of ${detail.filesChanged}` : ''}. Download the full{' '}
            <a href={diffUrl} download>
              .diff
            </a>{' '}
            or{' '}
            <a href={patchUrl} download>
              .patch
            </a>
            .
          </div>
        </div>
      ) : null}

      {detail.files.length === 0 ? (
        <p className="muted">{ignoreWs ? 'No changes besides whitespace.' : 'No file changes in this commit.'}</p>
      ) : (
        <DiffView files={detail.files} mode={mode} listOnly={detail.listOnly} fileHref={fileHref} />
      )}
    </div>
  );
}
