import { ChevronDown, ChevronRight } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import type { FileDiff } from '@gitstack/api-client';
import { parsePatch, toSplitRows } from '../lib/diffParse';
import type { DiffLine } from '../lib/diffParse';
import { Button } from './Button';

export type DiffViewMode = 'unified' | 'split';

export interface DiffViewProps {
  files: readonly FileDiff[];
  mode?: DiffViewMode;
  /** Solo l'elenco (B6, oltre i limiti): righe aggiunte e tolte, niente patch. */
  listOnly?: boolean;
  /** Indirizzo di «View file» per un file; null/assente: nessun link. */
  fileHref?: (file: FileDiff) => string | null;
}

const REASON_LABEL: Record<NonNullable<FileDiff['collapseReason']>, { badge: string; text: string }> = {
  large: { badge: 'Large diff', text: 'Large diffs (over 500 changed lines) are collapsed by default.' },
  lock: { badge: 'Lock file', text: 'Lock and generated files are collapsed by default.' },
  generated: { badge: 'Generated', text: 'Lock and generated files are collapsed by default.' },
};

const STATUS_LABEL: Record<FileDiff['status'], string> = {
  added: 'Added',
  modified: 'Modified',
  deleted: 'Deleted',
  renamed: 'Renamed',
  copied: 'Copied',
};

/** Cinque quadratini proporzionali alle righe aggiunte e tolte, come nel mockup 10. */
function diffstat(additions: number, deletions: number): ('a' | 'd' | '')[] {
  const total = additions + deletions;
  if (total === 0) return ['', '', '', '', ''];
  let a = Math.round((additions / total) * 5);
  if (additions > 0 && a === 0) a = 1;
  if (deletions > 0 && a === 5) a = 4;
  const d = Math.min(5 - a, deletions > 0 ? Math.max(1, 5 - a) : 0);
  return Array.from({ length: 5 }, (_, i) => (i < a ? 'a' : i < a + d ? 'd' : ''));
}

/**
 * Diff per file: unificato o affiancato, file chiusi con il motivo e «Load diff»,
 * binari, rinominati. Non conosce commit ne' pagine: riusabile per le PR.
 */
export function DiffView({ files, mode = 'unified', listOnly = false, fileHref }: DiffViewProps) {
  return (
    <div className="diff-list">
      {files.map((f) => (
        <DiffFile key={`${f.oldPath ?? ''}>${f.path}`} file={f} mode={mode} listOnly={listOnly} href={fileHref?.(f) ?? null} />
      ))}
    </div>
  );
}

function DiffFile({ file, mode, listOnly, href }: { file: FileDiff; mode: DiffViewMode; listOnly: boolean; href: string | null }) {
  const collapsedByApi = Boolean(file.collapsed) && !listOnly;
  const [loaded, setLoaded] = useState(false);
  const [open, setOpen] = useState(true);
  const reason = file.collapseReason ? REASON_LABEL[file.collapseReason] : null;
  const hidden = collapsedByApi && !loaded;
  const showBody = !listOnly && !hidden && open;
  const renamed = file.oldPath !== undefined && file.oldPath !== file.path;
  const Chev = showBody ? ChevronDown : ChevronRight;

  return (
    <section className="diff" aria-label={`Diff of ${file.path}`}>
      <div className="diff-h">
        {listOnly || hidden ? (
          <Chev size={14} aria-hidden="true" />
        ) : (
          <button type="button" className="diff-toggle" aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${file.path}`} onClick={() => setOpen((v) => !v)}>
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
        {href && file.status !== 'deleted' ? (
          <Link className="btn btn-sm" to={href}>
            View file
          </Link>
        ) : null}
      </div>
      {hidden && reason ? <div className="small muted diff-note">{reason.text}</div> : null}
      {showBody ? <DiffBody file={file} mode={mode} /> : null}
    </section>
  );
}

function DiffBody({ file, mode }: { file: FileDiff; mode: DiffViewMode }) {
  const lines = useMemo(() => (file.patch ? parsePatch(file.patch) : []), [file.patch]);
  const rows = useMemo(() => (mode === 'split' ? toSplitRows(lines) : []), [mode, lines]);
  if (file.binary) return <div className="small muted diff-note">Binary file not shown.</div>;
  if (file.patch === undefined || file.patch === '') {
    if (file.truncated) return <div className="small muted diff-note">Diff too large to show. Download the full .diff or .patch.</div>;
    return <div className="small muted diff-note">{file.status === 'renamed' || file.status === 'copied' ? 'File moved without changes.' : 'No textual changes.'}</div>;
  }
  return (
    <>
      {mode === 'split' ? (
        <div className="dsplit" role="table" aria-label={`Split diff of ${file.path}`}>
          {rows.map((r, i) =>
            r.span ? (
              <div key={i} className={`dl ${r.span.kind === 'hunk' ? 'hunk' : 'ctx'} span`} role="row">
                <div role="cell">{r.span.text}</div>
              </div>
            ) : (
              <div key={i} className="dl2" role="row">
                <Half line={r.left} side="left" />
                <Half line={r.right} side="right" />
              </div>
            ),
          )}
        </div>
      ) : (
        <div role="table" aria-label={`Unified diff of ${file.path}`}>
          {lines.map((l, i) => (
            <div key={i} className={`dl ${l.kind === 'note' ? 'ctx' : l.kind}`} role="row">
              <div className="n" role="cell">{l.oldNo ?? ''}</div>
              <div className="n" role="cell">{l.newNo ?? ''}</div>
              <div role="cell">{l.kind === 'hunk' || l.kind === 'note' ? l.text : `${l.kind === 'add' ? '+' : l.kind === 'del' ? '-' : ' '}${l.text}`}</div>
            </div>
          ))}
        </div>
      )}
      {file.truncated ? <div className="small muted diff-note">Diff truncated. Download the full .diff or .patch.</div> : null}
    </>
  );
}

function Half({ line, side }: { line: DiffLine | null; side: "left" | "right" }) {
  if (!line) return <div className="dh empty" role="presentation" />;
  const no = side === "left" ? line.oldNo : line.newNo;
  return (
    <div className={`dh ${line.kind}`}>
      <div className="n" role="cell">{no ?? ""}</div>
      <div role="cell">{line.text}</div>
    </div>
  );
}
