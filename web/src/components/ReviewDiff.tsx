import { Bot, ChevronDown, ChevronRight, Pencil } from 'lucide-react';
import { Fragment, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import type { FileDiff } from '@gitstack/api-client';
import { diffstat, REASON_LABEL, STATUS_LABEL } from '../lib/diffMeta';
import { parsePatch, toSplitRows } from '../lib/diffParse';
import type { DiffLine } from '../lib/diffParse';
import { timeAgo } from '../lib/format';
import type { DiffSide, PullRequestComment, PullRequestThread } from '../lib/pullsApi';
import { anchorKey, anchorOf, lineLabel, linesInRange, rangeLabel, splitSuggestions } from '../lib/reviewLines';
import type { Anchor } from '../lib/reviewLines';
import { Button } from './Button';
import { Checkbox } from './Checkbox';
import { Markdown } from './Markdown';
import type { DiffViewMode } from './DiffView';

export interface LineCommentInput {
  path: string;
  side: DiffSide;
  line: number;
  startLine?: number;
  body: string;
}

export interface ReviewDiffProps {
  files: readonly FileDiff[];
  mode: DiffViewMode;
  listOnly?: boolean;
  threads: readonly PullRequestThread[];
  viewed: ReadonlySet<string>;
  /** Si possono scrivere commenti (PR aperta, repo non archiviato). */
  canComment: boolean;
  /** L'autore della PR e chi ha write possono risolvere (PR5). */
  canResolve: boolean;
  onViewed: (path: string, viewed: boolean) => void;
  onComment: (input: LineCommentInput) => Promise<boolean>;
  onReply: (threadId: string, body: string) => Promise<boolean>;
  onResolve: (threadId: string, resolved: boolean) => void;
}

/** Diff della PR con commenti sulle righe, conversazioni, suggestion e casella Viewed (mockup 27). */
export function ReviewDiff({ files, listOnly = false, ...rest }: ReviewDiffProps) {
  return (
    <div className="diff-list">
      {files.map((f) => (
        <ReviewFile key={`${f.oldPath ?? ''}>${f.path}`} file={f} listOnly={listOnly} {...rest} />
      ))}
    </div>
  );
}

interface Selection {
  side: DiffSide;
  from: number;
  to: number;
}

type FileProps = Omit<ReviewDiffProps, 'files' | 'listOnly'> & { file: FileDiff; listOnly: boolean };

function ReviewFile({ file, mode, listOnly, threads, viewed, canComment, canResolve, onViewed, onComment, onReply, onResolve }: FileProps) {
  const collapsedByApi = Boolean(file.collapsed) && !listOnly;
  const [loaded, setLoaded] = useState(false);
  const [open, setOpen] = useState(true);
  const [showViewed, setShowViewed] = useState(false);
  const isViewed = viewed.has(file.path);
  const reason = file.collapseReason ? REASON_LABEL[file.collapseReason] : null;
  const hidden = collapsedByApi && !loaded;
  const showBody = !listOnly && !hidden && open && (!isViewed || showViewed);
  const renamed = file.oldPath !== undefined && file.oldPath !== file.path;
  const Chev = showBody ? ChevronDown : ChevronRight;
  const mine = threads.filter((t) => t.path === file.path);

  return (
    <section className="diff" aria-label={`Diff of ${file.path}`}>
      <div className="diff-h">
        {listOnly || hidden ? (
          <Chev size={14} aria-hidden="true" />
        ) : (
          <button type="button" className="diff-toggle" aria-expanded={showBody} aria-label={`${showBody ? 'Collapse' : 'Expand'} ${file.path}`} onClick={() => (isViewed && !showViewed ? setShowViewed(true) : setOpen((v) => !v))}>
            <Chev size={14} aria-hidden="true" />
          </button>
        )}
        <span className="mono">
          {renamed ? (
            <>
              <span className="muted">{file.oldPath}</span> → <b>{file.path}</b>
            </>
          ) : (
            <b>{file.path}</b>
          )}
        </span>
        {file.status !== 'modified' ? <span className="badge">{STATUS_LABEL[file.status]}</span> : null}
        {file.binary ? <span className="badge">Binary</span> : null}
        <span className="diffstat" aria-hidden="true">
          {diffstat(file.additions, file.deletions).map((k, i) => (
            <i key={i} className={k || undefined} />
          ))}
        </span>
        <span className="small muted">{file.binary ? 'binary' : `+${file.additions} −${file.deletions}`}</span>
        {reason && collapsedByApi ? <span className="badge">{reason.badge}</span> : null}
        <span className="sp" />
        {hidden ? (
          <Button size="sm" onClick={() => setLoaded(true)}>
            Load diff
          </Button>
        ) : null}
        <Checkbox
          label="Viewed"
          checked={isViewed}
          onCheckedChange={(v) => {
            setShowViewed(false);
            onViewed(file.path, v);
          }}
        />
      </div>
      {hidden && reason ? <div className="small muted diff-note">{reason.text}</div> : null}
      {isViewed && !showViewed && !listOnly ? (
        <>
          {mine.length > 0 ? <DetachedThreads threads={mine.filter((t) => t.resolved && t.outdated)} {...{ canComment, canResolve, onReply, onResolve }} original={[]} /> : null}
          <div className="small muted diff-note">
            File marked as viewed.{' '}
            <button type="button" className="linklike" onClick={() => setShowViewed(true)}>
              Show diff
            </button>
          </div>
        </>
      ) : null}
      {showBody ? <FileBody file={file} mode={mode} threads={mine} canComment={canComment} canResolve={canResolve} onComment={onComment} onReply={onReply} onResolve={onResolve} /> : null}
    </section>
  );
}

interface BodyProps {
  file: FileDiff;
  mode: DiffViewMode;
  threads: PullRequestThread[];
  canComment: boolean;
  canResolve: boolean;
  onComment: ReviewDiffProps['onComment'];
  onReply: ReviewDiffProps['onReply'];
  onResolve: ReviewDiffProps['onResolve'];
}

function FileBody({ file, mode, threads, canComment, canResolve, onComment, onReply, onResolve }: BodyProps) {
  const lines = useMemo(() => (file.patch ? parsePatch(file.patch) : []), [file.patch]);
  const rows = useMemo(() => (mode === 'split' ? toSplitRows(lines) : []), [mode, lines]);
  const [sel, setSel] = useState<Selection | null>(null);
  const [last, setLast] = useState<Anchor | null>(null);

  // Conversazioni sulle righe vive stanno sotto la riga finale; le outdated o senza riga nel diff, in cima.
  const { byLine, detached } = useMemo(() => {
    const present = new Set<string>();
    for (const l of lines) {
      const a = anchorOf(l);
      if (a) present.add(anchorKey(a.side, a.no));
    }
    const map = new Map<string, PullRequestThread[]>();
    const loose: PullRequestThread[] = [];
    for (const t of threads) {
      const key = anchorKey(t.side, t.line);
      if (t.outdated || !present.has(key)) loose.push(t);
      else map.set(key, [...(map.get(key) ?? []), t]);
    }
    return { byLine: map, detached: loose };
  }, [lines, threads]);

  if (file.binary) return <div className="small muted diff-note">Binary file not shown.</div>;
  if (file.patch === undefined || file.patch === '') {
    if (file.truncated) return <div className="small muted diff-note">Diff too large to show. Download the full .diff or .patch.</div>;
    return (
      <>
        <DetachedThreads threads={detached} {...{ canComment, canResolve, onReply, onResolve }} original={lines} />
        <div className="small muted diff-note">{file.status === 'renamed' || file.status === 'copied' ? 'File moved without changes.' : 'No textual changes.'}</div>
      </>
    );
  }

  const pick = (a: Anchor, shift: boolean) => {
    if (shift && sel && last && last.side === a.side) {
      setSel({ side: a.side, from: Math.min(last.no, a.no), to: Math.max(last.no, a.no) });
    } else {
      setSel({ side: a.side, from: a.no, to: a.no });
      setLast(a);
    }
  };
  const inSel = (a: Anchor | null) => Boolean(sel && a && a.side === sel.side && a.no >= sel.from && a.no <= sel.to);
  const selText = () => (sel ? linesInRange(lines, { side: sel.side, line: sel.to, startLine: sel.from }).map((l) => l.text) : []);

  const lineButton = (a: Anchor | null, shown: number | undefined) =>
    canComment && a ? (
      <button
        type="button"
        className="ln-btn"
        aria-label={`Comment on ${a.side === 'old' ? 'old ' : ''}line ${a.no}`}
        onClick={(e) => pick(a, e.shiftKey)}
      >
        {shown}
      </button>
    ) : (
      (shown ?? '')
    );

  // Sotto la riga (o la coppia di righe) con queste ancore: compositore e conversazioni.
  const after = (anchors: (Anchor | null)[]): ReactNode => {
    const keys = anchors.filter((a): a is Anchor => a !== null).map((a) => anchorKey(a.side, a.no));
    const here = keys.flatMap((k) => byLine.get(k) ?? []);
    const composing = sel && keys.includes(anchorKey(sel.side, sel.to));
    if (!composing && here.length === 0) return null;
    return (
      <>
        {here.map((t) => (
          <ThreadView key={t.id} thread={t} original={lines} {...{ canComment, canResolve, onReply, onResolve }} />
        ))}
        {composing && sel ? (
          <Composer
            key={`${sel.side}:${sel.from}-${sel.to}`}
            sel={sel}
            suggestionFrom={selText()}
            onCancel={() => setSel(null)}
            onSubmit={async (body) => {
              const ok = await onComment({ path: file.path, side: sel.side, line: sel.to, ...(sel.from < sel.to ? { startLine: sel.from } : {}), body });
              if (ok) setSel(null);
              return ok;
            }}
          />
        ) : null}
      </>
    );
  };

  return (
    <>
      <DetachedThreads threads={detached} {...{ canComment, canResolve, onReply, onResolve }} original={lines} />
      {mode === 'split' ? (
        <div className="dsplit" role="table" aria-label={`Split diff of ${file.path}`}>
          {rows.map((r, i) =>
            r.span ? (
              <div key={i} className={`dl ${r.span.kind === 'hunk' ? 'hunk' : 'ctx'} span`} role="row">
                <div role="cell">{r.span.text}</div>
              </div>
            ) : (
              <Fragment key={i}>
                <div className="dl2" role="row">
                  <SplitHalf line={r.left} side="left" selected={inSel(r.left && r.left.kind === 'del' ? anchorOf(r.left) : null)} button={lineButton} />
                  <SplitHalf line={r.right} side="right" selected={inSel(r.right ? anchorOf(r.right) : null)} button={lineButton} />
                </div>
                {after([r.left && r.left.kind === 'del' ? anchorOf(r.left) : null, r.right ? anchorOf(r.right) : null])}
              </Fragment>
            ),
          )}
        </div>
      ) : (
        <div role="table" aria-label={`Unified diff of ${file.path}`}>
          {lines.map((l, i) => {
            const a = anchorOf(l);
            return (
              <Fragment key={i}>
                <div className={`dl ${l.kind === 'note' ? 'ctx' : l.kind}${inSel(a) ? ' sel' : ''}`} role="row">
                  <div className="n" role="cell">{l.kind === 'del' ? lineButton(a, l.oldNo) : (l.oldNo ?? '')}</div>
                  <div className="n" role="cell">{l.kind === 'del' ? '' : l.kind === 'hunk' || l.kind === 'note' ? '' : lineButton(a, l.newNo)}</div>
                  <div role="cell">{l.kind === 'hunk' || l.kind === 'note' ? l.text : `${l.kind === 'add' ? '+' : l.kind === 'del' ? '-' : ' '}${l.text}`}</div>
                </div>
                {after([a])}
              </Fragment>
            );
          })}
        </div>
      )}
      {file.truncated ? <div className="small muted diff-note">Diff truncated. Download the full .diff or .patch.</div> : null}
    </>
  );
}

function SplitHalf({
  line,
  side,
  selected,
  button,
}: {
  line: DiffLine | null;
  side: 'left' | 'right';
  selected: boolean;
  button: (a: Anchor | null, shown: number | undefined) => ReactNode;
}) {
  if (!line) return <div className="dh empty" role="presentation" />;
  const no = side === 'left' ? line.oldNo : line.newNo;
  // A sinistra si commentano solo le righe tolte; il contesto si commenta a destra (lato new).
  const commentable = side === 'left' ? line.kind === 'del' : true;
  return (
    <div className={`dh ${line.kind}${selected ? ' sel' : ''}`}>
      <div className="n" role="cell">{commentable ? button(anchorOf(line), no) : (no ?? '')}</div>
      <div role="cell">{line.text}</div>
    </div>
  );
}

function Avatar({ author }: { author: PullRequestComment['author'] }) {
  return author.kind === 'agent' ? (
    <span className="avatar av-bot" aria-hidden="true">
      <Bot size={14} />
    </span>
  ) : (
    <span className="avatar" aria-hidden="true">
      {author.username.slice(0, 2).toUpperCase()}
    </span>
  );
}

interface ThreadActions {
  canComment: boolean;
  canResolve: boolean;
  onReply: (threadId: string, body: string) => Promise<boolean>;
  onResolve: (threadId: string, resolved: boolean) => void;
}

function DetachedThreads({ threads, original, ...actions }: ThreadActions & { threads: readonly PullRequestThread[]; original: readonly DiffLine[] }) {
  if (threads.length === 0) return null;
  return (
    <>
      {threads.map((t) => (
        <ThreadView key={t.id} thread={t} original={original} {...actions} />
      ))}
    </>
  );
}

/** Conversazione sulle righe: risolta o outdated si comprime; si riapre con «Show conversation». */
function ThreadView({ thread: t, original, canComment, canResolve, onReply, onResolve }: ThreadActions & { thread: PullRequestThread; original: readonly DiffLine[] }) {
  const [open, setOpen] = useState(!t.resolved && !t.outdated);
  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState(false);
  const first = t.comments[0];
  const orig = linesInRange(original, t);

  const badges = (
    <>
      {t.outdated ? <span className="badge">Outdated</span> : null}
      {t.resolved ? (
        <span className="badge" style={{ borderColor: 'var(--success)', color: 'var(--success)' }}>
          Resolved
        </span>
      ) : null}
    </>
  );

  if (!open) {
    return (
      <div className={`thread${t.outdated ? ' outdated' : ''} collapsed`} role="group" aria-label={`Conversation on ${t.path} ${rangeLabel(t.side, t.startLine, t.line)}`}>
        <div className="th-h">
          {badges}
          <span className="small muted">
            {first ? <b style={{ color: 'var(--text)' }}>{first.author.username}</b> : null}
            {t.outdated ? ' on a line that changed in a later commit' : ` on ${rangeLabel(t.side, t.startLine, t.line)}`}
          </span>
          <span className="sp" style={{ flex: 1 }} />
          <button type="button" className="linklike small" onClick={() => setOpen(true)}>
            Show conversation
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={`thread${t.outdated ? ' outdated' : ''}`} role="group" aria-label={`Conversation on ${t.path} ${rangeLabel(t.side, t.startLine, t.line)}`}>
      <div className="th-h">
        {badges}
        <span className="small muted">
          Comment on {rangeLabel(t.side, t.startLine, t.line)}
        </span>
        <span className="sp" style={{ flex: 1 }} />
        {t.resolved ? (
          <span className="small muted">Resolved{t.resolvedBy ? ` by ${t.resolvedBy.username}` : ''}</span>
        ) : null}
        {t.resolved || t.outdated ? (
          <button type="button" className="linklike small" onClick={() => setOpen(false)}>
            Hide conversation
          </button>
        ) : null}
        {canResolve ? (
          <Button size="sm" onClick={() => onResolve(t.id, !t.resolved)}>
            {t.resolved ? 'Unresolve conversation' : 'Resolve conversation'}
          </Button>
        ) : null}
      </div>
      {t.comments
        .filter((c) => !c.deleted)
        .map((c) => (
          <div className="th-c" key={c.id}>
            <Avatar author={c.author} />
            <div style={{ minWidth: 0, flex: 1 }}>
              <b>{c.author.username}</b>{' '}
              {c.author.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null}{' '}
              <span className="muted small">{timeAgo(c.createdAt)}</span>
              {c.outdated && !t.outdated ? <span className="badge">Outdated</span> : null}
              <CommentBody body={c.body} original={orig} t={t} />
            </div>
          </div>
        ))}
      {canComment ? (
        <div className="th-r">
          <form
            className="row"
            onSubmit={(e) => {
              e.preventDefault();
              if (!reply.trim() || busy) return;
              setBusy(true);
              void onReply(t.id, reply.trim())
                .then((ok) => {
                  if (ok) setReply('');
                })
                .finally(() => setBusy(false));
            }}
          >
            <input className="input" style={{ flex: 1 }} aria-label={`Reply to ${first?.author.username ?? 'conversation'}`} placeholder="Reply…" value={reply} onChange={(e) => setReply(e.target.value)} />
            <Button type="submit" size="sm" disabled={busy || !reply.trim()}>
              Reply
            </Button>
          </form>
        </div>
      ) : null}
    </div>
  );
}

/** Testo in Markdown; i blocchi ```suggestion diventano un diff (righe tolte e proposte), senza «Applica». */
function CommentBody({ body, original, t }: { body: string; original: readonly DiffLine[]; t: PullRequestThread }) {
  const parts = useMemo(() => splitSuggestions(body), [body]);
  return (
    <>
      {parts.map((p, i) =>
        p.kind === 'text' ? (
          <Markdown key={i} source={p.text} />
        ) : (
          <Suggestion key={i} code={p.code} original={original} t={t} />
        ),
      )}
    </>
  );
}

function Suggestion({ code, original, t }: { code: string[]; original: readonly DiffLine[]; t: PullRequestThread }) {
  const start = t.startLine ?? t.line;
  return (
    <div className="suggest" role="group" aria-label="Suggested change">
      <div className="sg-h">
        <Pencil size={14} aria-hidden="true" />
        Suggested change
        <span className="sp" style={{ flex: 1 }} />
        <span className="small muted" title="Applying suggestions from the browser comes after v1.1">
          Apply it with a commit
        </span>
      </div>
      {original.map((l, i) => (
        <div className="dl del" role="row" key={`d${i}`}>
          <div className="n">{t.side === 'old' ? l.oldNo : l.newNo}</div>
          <div className="n" />
          <div>{`-${l.text}`}</div>
        </div>
      ))}
      {code.map((text, i) => (
        <div className="dl add" role="row" key={`a${i}`}>
          <div className="n" />
          <div className="n">{start + i}</div>
          <div>{`+${text}`}</div>
        </div>
      ))}
    </div>
  );
}

function Composer({ sel, suggestionFrom, onCancel, onSubmit }: { sel: Selection; suggestionFrom: string[]; onCancel: () => void; onSubmit: (body: string) => Promise<boolean> }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const label = `Comment on ${rangeLabel(sel.side, sel.from, sel.to)}`;
  return (
    <div className="thread composer" role="group" aria-label={label}>
      <div className="th-h">
        <span className="small muted">
          Comment on {sel.from < sel.to ? 'lines' : 'line'} <b>{sel.from < sel.to ? `${lineLabel(sel.side, sel.from)} to ${lineLabel(sel.side, sel.to)}` : lineLabel(sel.side, sel.to)}</b>
        </span>
        <span className="sp" style={{ flex: 1 }} />
        {sel.side === 'new' ? (
          <button type="button" className="linklike small" onClick={() => setText((v) => `${v}${v && !v.endsWith('\n') ? '\n' : ''}\`\`\`suggestion\n${suggestionFrom.join('\n')}\n\`\`\`\n`)}>
            Suggest a change
          </button>
        ) : null}
      </div>
      <div className="th-r">
        <textarea className="textarea" aria-label={label} placeholder="Leave a comment" value={text} onChange={(e) => setText(e.target.value)} />
        <div className="row" style={{ marginTop: 8 }}>
          <span className="sp" style={{ flex: 1 }} />
          <Button size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            size="sm"
            variant="primary"
            disabled={busy || !text.trim()}
            onClick={() => {
              setBusy(true);
              void onSubmit(text.trim()).finally(() => setBusy(false));
            }}
          >
            Add comment
          </Button>
        </div>
      </div>
    </div>
  );
}
