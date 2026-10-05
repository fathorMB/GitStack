import { Bot, Download, GitBranch, History } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { MouseEvent, ReactNode } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, CopyButton, ErrorAlert, Markdown } from '../../components';
import {
  blameHref,
  blobHref,
  commitHref,
  decodeContent,
  fetchBlame,
  fetchBranches,
  fetchFile,
  fetchTags,
  historyHref,
  isMarkdownName,
  languageFor,
  rawHref,
  splitRefPath,
  treeHref,
} from '../../lib/codeApi';
import type { Blame, CommitSummary, FileContent } from '../../lib/codeApi';
import { formatBytes, timeAgo } from '../../lib/format';
import { lineHash, parseLineHash, splitLines, textLines } from '../../lib/codeLines';
import { highlightCode } from '../../lib/highlight';
import { ApiError } from '../../lib/http';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

export type FileMode = 'blob' | 'blame';

/** B1: oltre questa soglia il blame non c'e' (blameMaxBytes). */
export const BLAME_MAX_BYTES = 1048576;

/** Righe evidenziate (display=highlight); finche' la libreria non e' pronta, testo semplice. */
function useHighlightedLines(content: string | null, fileName: string, enabled: boolean): ReactNode[][] {
  const plain = useMemo(() => (content === null ? [] : textLines(content).map((l) => [l] as ReactNode[])), [content]);
  const [done, setDone] = useState<{ content: string; lines: ReactNode[][] } | null>(null);
  useEffect(() => {
    const lang = languageFor(fileName);
    if (!enabled || content === null || !lang) return;
    let live = true;
    void highlightCode(content, lang)
      .then((nodes) => {
        if (!live) return;
        const lines = splitLines(nodes);
        if (lines.length > 1 && lines[lines.length - 1]?.length === 0) lines.pop();
        setDone({ content, lines });
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [content, fileName, enabled]);
  return done && done.content === content ? done.lines : plain;
}

/** Pagina file (mockup 08) e blame (mockup 22): `splat` e' `<ref>/<percorso>`. */
export function FileView({ repo, splat, mode }: { repo: Repository; splat: string; mode: FileMode }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data: names, error } = useLoad<string[]>(async () => {
    const [b, t] = await Promise.all([fetchBranches(owner, name), fetchTags(owner, name)]);
    return [...b.items.map((x) => x.name), ...t.items.map((x) => x.name)];
  });
  if (error) return <ErrorAlert message={error} />;
  if (!names) return <p className="muted">Loading file…</p>;
  const { ref, path } = splitRefPath(splat, names);
  if (path === '') return <ErrorAlert message="File not found." />;
  return <FileLoader key={`${ref}:${path}`} repo={repo} refName={ref} path={path} mode={mode} />;
}

function FileLoader({ repo, refName, path, mode }: { repo: Repository; refName: string; path: string; mode: FileMode }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data: file, error } = useLoad(() => fetchFile(owner, name, refName, path));
  const defaultRef = repo.defaultBranch || 'main';
  const parts = path.split('/');
  return (
    <div className="stack" style={{ gap: 14 }}>
      <div className="row code-bar">
        <span className="btn" aria-label="Current ref">
          <GitBranch size={14} aria-hidden="true" /> <span className="mono">{refName}</span>
        </span>
        <nav className="crumbs mono" aria-label="Breadcrumb" style={{ fontSize: 15 }}>
          <Link to={treeHref(owner, name, refName, '', defaultRef)}>{name}</Link>
          {parts.map((part, i) => {
            const sub = parts.slice(0, i + 1).join('/');
            return (
              <span key={sub} className="row" style={{ gap: 6 }}>
                <span className="subtle">/</span>
                {i === parts.length - 1 ? <b aria-current="page">{part}</b> : <Link to={treeHref(owner, name, refName, sub, defaultRef)}>{part}</Link>}
              </span>
            );
          })}
        </nav>
      </div>
      {error ? <ErrorAlert message={error} /> : null}
      {!file && !error ? <p className="muted">Loading file…</p> : null}
      {file ? <FileCard owner={owner} repo={name} refName={refName} file={file} mode={mode} /> : null}
    </div>
  );
}

function AuthorLine({ commit }: { commit: CommitSummary }) {
  const agent = commit.author.user?.kind === 'agent';
  return (
    <>
      <span className={`avatar${agent ? ' av-bot' : ''}`} aria-hidden="true">
        {agent ? <Bot size={14} /> : commit.author.name.slice(0, 1).toUpperCase()}
      </span>
      <b>{commit.author.user?.username ?? commit.author.name}</b>
      {agent ? <span className="badge badge-agent">agent</span> : null}
    </>
  );
}

function FileCard({ owner, repo, refName, file, mode }: { owner: string; repo: string; refName: string; file: FileContent; mode: FileMode }) {
  const isText = file.kind === 'text' && (file.display === 'highlight' || file.display === 'plain') && file.content !== undefined;
  const text = useMemo(() => (isText ? decodeContent(file) : null), [file, isText]);
  const lineCount = text === null ? null : textLines(text).length;
  const markdown = isText && isMarkdownName(file.name);
  const [showSource, setShowSource] = useState(false);
  const blameOk = file.kind === 'text' && file.size <= BLAME_MAX_BYTES && file.display === 'highlight';
  const raw = rawHref(owner, repo, refName, file.path);
  const blobTo = blobHref(owner, repo, refName, file.path);
  const blameTo = blameHref(owner, repo, refName, file.path);
  const last = file.lastCommit;

  return (
    <section className="codeview" aria-label={mode === 'blame' ? 'Blame' : 'File'}>
      <div className="lastcommit" aria-label="Latest commit">
        <AuthorLine commit={last} />
        <span className="msg">{last.subject}</span>
        <Link className="mono small" to={commitHref(owner, repo, last.sha)}>
          {last.sha.slice(0, 7)}
        </Link>
        <span className="small subtle nowrap">{timeAgo(last.committer.date)}</span>
      </div>
      <div className="codeview-h">
        <span className="mono" aria-label="File info">
          {lineCount !== null ? `${lineCount} ${lineCount === 1 ? 'line' : 'lines'} · ` : ''}
          {formatBytes(file.size)}
        </span>
        <span className="sp" />
        <span className="seg" role="group" aria-label="View">
          <Link to={blobTo} className={mode === 'blob' ? 'active' : undefined} aria-current={mode === 'blob' ? 'page' : undefined}>
            Code
          </Link>
          <Link to={blameTo} className={mode === 'blame' ? 'active' : undefined} aria-current={mode === 'blame' ? 'page' : undefined}>
            Blame
          </Link>
        </span>
        {markdown && mode === 'blob' ? (
          <Button size="sm" aria-pressed={showSource} onClick={() => setShowSource((v) => !v)}>
            {showSource ? 'Preview' : 'Source'}
          </Button>
        ) : null}
        <Link className="btn btn-sm" to={historyHref(owner, repo, refName, file.path)}>
          <History size={14} aria-hidden="true" /> History
        </Link>
        <a className="btn btn-sm" href={raw}>
          Raw
        </a>
        {text !== null ? <CopyButton value={text} label="Copy" /> : null}
        <a className="btn btn-sm" href={raw} download={file.name} aria-label="Download">
          <Download size={14} aria-hidden="true" />
        </a>
      </div>
      {mode === 'blame' ? (
        <BlameBody owner={owner} repo={repo} refName={refName} file={file} text={text} blameOk={blameOk} />
      ) : (
        <FileBody owner={owner} repo={repo} refName={refName} file={file} text={text} markdown={markdown && !showSource} raw={raw} />
      )}
    </section>
  );
}

function FileBody({ owner, repo, refName, file, text, markdown, raw }: { owner: string; repo: string; refName: string; file: FileContent; text: string | null; markdown: boolean; raw: string }) {
  if (text !== null) {
    if (markdown) {
      const dir = file.path.includes('/') ? file.path.slice(0, file.path.lastIndexOf('/')) : '';
      return (
        <div className="readme">
          <Markdown
            source={text}
            basePath={dir}
            repo={{ owner, name: repo }}
            resolveLink={(p) => blobHref(owner, repo, refName, p)}
            resolveImage={(p) => rawHref(owner, repo, refName, p)}
          />
        </div>
      );
    }
    return <CodeLines text={text} fileName={file.name} highlight={file.display === 'highlight'} />;
  }
  if (file.display === 'image' && file.content && file.mimeType) {
    // Sempre e solo <img>: un SVG non entra mai nella pagina come markup.
    return (
      <div className="codeview-img">
        <img src={`data:${file.mimeType};base64,${file.content}`} alt={file.name} />
      </div>
    );
  }
  let msg = 'Binary file';
  if (file.kind === 'text') msg = 'File too large to display';
  else if (file.kind === 'image') msg = 'Image too large to display';
  return (
    <div className="codeview-empty">
      <p>
        <b>{msg}</b>
      </p>
      <p className="small">{formatBytes(file.size)}</p>
      <a className="btn" href={raw} download={file.name}>
        <Download size={14} aria-hidden="true" /> Download
      </a>
    </div>
  );
}

function useSelection(): [number, number] | null {
  const { hash } = useLocation();
  return useMemo(() => parseLineHash(hash), [hash]);
}

function LineNumber({ n, anchor, onPick }: { n: number; anchor: number | null; onPick: (n: number, extend: boolean) => void }) {
  return (
    <a
      className="lno"
      id={`L${n}`}
      href={`#L${n}`}
      aria-label={`Line ${n}`}
      onClick={(e: MouseEvent) => {
        e.preventDefault();
        onPick(n, e.shiftKey && anchor !== null);
      }}
    >
      {n}
    </a>
  );
}

function useLinePicker(): { sel: [number, number] | null; pick: (n: number, extend: boolean) => void } {
  const sel = useSelection();
  const navigate = useNavigate();
  const pick = (n: number, extend: boolean) => {
    const from = sel ? sel[0] : n;
    navigate({ hash: extend ? lineHash(from, n) : lineHash(n, n) }, { replace: true });
  };
  return { sel, pick };
}

/** Porta in vista la prima riga selezionata quando l'ancora cambia da link. */
function useScrollToSelection(sel: [number, number] | null, ready: boolean) {
  const done = useRef<string>('');
  useEffect(() => {
    if (!sel || !ready) return;
    const key = `${sel[0]}`;
    if (done.current === key) return;
    done.current = key;
    document.getElementById(`L${sel[0]}`)?.scrollIntoView?.({ block: 'center' });
  }, [sel, ready]);
}

function CodeLines({ text, fileName, highlight }: { text: string; fileName: string; highlight: boolean }) {
  const lines = useHighlightedLines(text, fileName, highlight);
  const { sel, pick } = useLinePicker();
  useScrollToSelection(sel, lines.length > 0);
  return (
    <div className="codelines" data-highlight={highlight ? 'true' : 'false'}>
      {lines.map((line, i) => {
        const n = i + 1;
        const selected = sel !== null && n >= sel[0] && n <= sel[1];
        return (
          <div key={n} className={`codeline${selected ? ' sel' : ''}`} data-selected={selected ? 'true' : undefined}>
            <LineNumber n={n} anchor={sel ? sel[0] : null} onPick={pick} />
            <pre className="lsrc">{line.length > 0 ? line : '\n'}</pre>
          </div>
        );
      })}
    </div>
  );
}

function BlameBody({ owner, repo, refName, file, text, blameOk }: { owner: string; repo: string; refName: string; file: FileContent; text: string | null; blameOk: boolean }) {
  if (!blameOk || text === null) {
    return <div className="codeview-empty">Blame is available for text files up to 1 MB.</div>;
  }
  return <BlameLines owner={owner} repo={repo} refName={refName} file={file} text={text} />;
}

function BlameLines({ owner, repo, refName, file, text }: { owner: string; repo: string; refName: string; file: FileContent; text: string }) {
  const { data, error } = useLoad<Blame | 'unavailable'>(async () => {
    try {
      return await fetchBlame(owner, repo, refName, file.path);
    } catch (err) {
      if (err instanceof ApiError && err.status === 400) return 'unavailable';
      throw err;
    }
  });
  const lines = useHighlightedLines(text, file.name, true);
  const { sel, pick } = useLinePicker();
  useScrollToSelection(sel, data !== null && data !== 'unavailable');
  if (error) return <ErrorAlert message={error} />;
  if (!data) return <p className="muted pad">Loading blame…</p>;
  if (data === 'unavailable') return <div className="codeview-empty">Blame is not available for this file.</div>;
  return (
    <div className="blame">
      {data.ranges.map((r) => {
        const agent = r.commit.author.user?.kind === 'agent';
        return (
          <div key={`${r.startLine}-${r.endLine}`} className="blame-row" aria-label={`Lines ${r.startLine}-${r.endLine}`}>
            <div className="blame-meta">
              <div className="row small">
                <span className={`avatar${agent ? ' av-bot' : ''}`} aria-hidden="true">
                  {agent ? <Bot size={11} /> : r.commit.author.name.slice(0, 2).toUpperCase()}
                </span>
                <b>{r.commit.author.user?.username ?? r.commit.author.name}</b>
                {agent ? <span className="badge badge-agent">agent</span> : null}
                <span className="subtle">{timeAgo(r.commit.author.date)}</span>
              </div>
              <Link className="mono small muted" to={commitHref(owner, repo, r.commit.sha)}>
                {r.commit.sha.slice(0, 7)} {r.commit.subject}
              </Link>
            </div>
            <div className="blame-code codelines">
              {lines.slice(r.startLine - 1, r.endLine).map((line, i) => {
                const n = r.startLine + i;
                const selected = sel !== null && n >= sel[0] && n <= sel[1];
                return (
                  <div key={n} className={`codeline${selected ? ' sel' : ''}`} data-selected={selected ? 'true' : undefined}>
                    <LineNumber n={n} anchor={sel ? sel[0] : null} onPick={pick} />
                    <pre className="lsrc">{line.length > 0 ? line : '\n'}</pre>
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
