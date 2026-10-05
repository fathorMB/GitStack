import { Bot, Info, Paperclip, Users, X } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import type { ChangeEvent, ClipboardEvent, DragEvent, KeyboardEvent, ReactNode } from 'react';
import { Button, ErrorAlert, Markdown } from '../../components';
import { formatBytes } from '../../lib/format';
import { describeError } from '../../lib/http';
import { MAX_ATTACHMENT_BYTES, uploadAttachment } from '../../lib/issuesApi';
import type { IssueAttachment } from '../../lib/issuesApi';
import { applyMention, mentionAt } from '../../lib/mentions';
import type { MentionCandidate, MentionSource } from '../../lib/mentions';

export interface IssueEditorProps {
  owner: string;
  repo: string;
  /** Etichetta accessibile della textarea. */
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  /** Con una sorgente, dopo «@» compaiono i suggerimenti di menzione (I8); senza prefisso nessuna chiamata. */
  mentions?: MentionSource;
  /** Altezza minima della textarea (mockup 20: 220px). */
  minHeight?: number;
  /** Con false niente allegati (es. modifica di un commento: il contratto non li accetta). */
  attachments?: boolean;
  /** Pulsanti a destra del footer; ricevono gli id degli allegati caricati. */
  actions: (ctx: { attachmentIds: string[]; clear: () => void }) => ReactNode;
}

/**
 * Editor Write/Preview (mockup 13): anteprima con il componente Markdown,
 * allegati fino a 10 MB (I9) caricati a parte e collegati con attachmentIds.
 */
export function IssueEditor({ owner, repo, label, value, onChange, placeholder, mentions, minHeight, attachments = true, actions }: IssueEditorProps) {
  const [tab, setTab] = useState<'write' | 'preview'>('write');
  const [files, setFiles] = useState<IssueAttachment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const listId = useId();
  const [token, setToken] = useState<{ start: number; prefix: string; caret: number } | null>(null);
  const [cands, setCands] = useState<MentionCandidate[]>([]);
  const [active, setActive] = useState(0);

  const prefix = token?.prefix ?? '';
  useEffect(() => {
    if (!mentions || !prefix) {
      setCands([]);
      return;
    }
    let live = true;
    mentions(prefix).then(
      (list) => {
        if (!live) return;
        setCands(list);
        setActive(0);
      },
      () => live && setCands([]),
    );
    return () => {
      live = false;
    };
  }, [mentions, prefix]);

  const track = (text: string, caret: number) => {
    const m = mentions ? mentionAt(text, caret) : null;
    setToken(m ? { ...m, caret } : null);
  };
  const pick = (c: MentionCandidate) => {
    if (!token) return;
    const next = applyMention(value, token.caret, token.start, c.handle);
    onChange(next.text);
    setToken(null);
    setCands([]);
    requestAnimationFrame(() => {
      area.current?.focus();
      area.current?.setSelectionRange(next.caret, next.caret);
    });
  };
  const open = cands.length > 0 && token !== null && token.prefix !== '';
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (i + 1) % cands.length);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (i - 1 + cands.length) % cands.length);
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault();
      pick(cands[active]);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      setToken(null);
      setCands([]);
    }
  };

  const add = async (list: File[]) => {
    setError(null);
    for (const f of list) {
      if (f.size > MAX_ATTACHMENT_BYTES) {
        setError(`${f.name} is ${formatBytes(f.size)}: attachments are limited to 10 MB.`);
        continue;
      }
      setBusy(true);
      try {
        const a = await uploadAttachment(owner, repo, f);
        setFiles((cur) => [...cur, a]);
      } catch (err) {
        setError(describeError(err));
      } finally {
        setBusy(false);
      }
    }
  };

  const onPick = (e: ChangeEvent<HTMLInputElement>) => {
    const list = Array.from(e.target.files ?? []);
    e.target.value = '';
    void add(list);
  };
  const onDrop = (e: DragEvent) => {
    if (!attachments || e.dataTransfer.files.length === 0) return;
    e.preventDefault();
    void add(Array.from(e.dataTransfer.files));
  };
  const onPaste = (e: ClipboardEvent) => {
    if (!attachments || e.clipboardData.files.length === 0) return;
    e.preventDefault();
    void add(Array.from(e.clipboardData.files));
  };

  return (
    <div className="editor" onDrop={onDrop} onDragOver={(e) => attachments && e.preventDefault()}>
      <div className="tabs" role="tablist" aria-label={`${label} mode`}>
        <button type="button" role="tab" aria-selected={tab === 'write'} className={tab === 'write' ? 'tab active' : 'tab'} onClick={() => setTab('write')}>
          Write
        </button>
        <button type="button" role="tab" aria-selected={tab === 'preview'} className={tab === 'preview' ? 'tab active' : 'tab'} onClick={() => setTab('preview')}>
          Preview
        </button>
      </div>
      {tab === 'write' ? (
        <div className="mention-wrap">
          <textarea
            ref={area}
            className="textarea"
            aria-label={label}
            placeholder={placeholder}
            value={value}
            style={minHeight ? { minHeight, fontFamily: 'var(--font-mono)', fontSize: 13 } : undefined}
            onChange={(e) => {
              onChange(e.target.value);
              track(e.target.value, e.target.selectionStart);
            }}
            onKeyDown={onKeyDown}
            onClick={(e) => track(e.currentTarget.value, e.currentTarget.selectionStart)}
            onPaste={onPaste}
            aria-autocomplete={mentions ? 'list' : undefined}
            aria-controls={open ? listId : undefined}
            aria-activedescendant={open ? `${listId}-${active}` : undefined}
          />
          {open ? (
            <ul id={listId} role="listbox" aria-label="Mention suggestions" className="menu mention-list">
              {cands.map((c, i) => (
                <li
                  key={c.handle}
                  id={`${listId}-${i}`}
                  role="option"
                  aria-selected={i === active}
                  className="mention-item"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    pick(c);
                  }}
                >
                  {c.kind === 'agent' ? <Bot size={14} aria-hidden="true" /> : c.kind === 'team' ? <Users size={14} aria-hidden="true" /> : null}
                  <b>@{c.handle}</b>
                  {c.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null}
                  {c.kind === 'team' ? <span className="badge">team</span> : null}
                  {c.label && c.label !== c.handle ? <span className="muted small">{c.label}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : (
        <div className="editor-preview" aria-label={`${label} preview`}>
          {value.trim() ? <Markdown source={value} repo={{ owner, name: repo }} /> : <span className="muted">Nothing to preview</span>}
        </div>
      )}
      {error ? <ErrorAlert message={error} /> : null}
      {files.length > 0 ? (
        <div className="editor-f" aria-label="Attachments">
          {files.map((f) => (
            <span key={f.id} className="row small" style={{ gap: 6, padding: '3px 8px', border: '1px solid var(--border)', borderRadius: 'var(--r)', background: 'var(--surface-2)' }}>
              <Paperclip size={14} aria-hidden="true" />
              <span className="mono">{f.filename}</span>
              <span className="muted">{formatBytes(f.size)}</span>
              <button type="button" className="btn btn-ghost btn-sm" aria-label={`Remove ${f.filename}`} onClick={() => setFiles((cur) => cur.filter((x) => x.id !== f.id))}>
                <X size={12} aria-hidden="true" />
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className="editor-f">
        <Info size={14} aria-hidden="true" />
        <span>{attachments ? 'Markdown supported · paste or drop images, PDF, logs, ZIP (max 10 MB)' : 'Markdown supported'}</span>
        {attachments ? (
          <>
            <input ref={input} type="file" hidden aria-label="Attach files" multiple onChange={onPick} />
            <Button size="sm" disabled={busy} onClick={() => input.current?.click()}>
              <Paperclip size={14} aria-hidden="true" /> Attach
            </Button>
          </>
        ) : null}
        <span className="sp" style={{ flex: 1 }} />
        {actions({
          attachmentIds: files.map((f) => f.id),
          clear: () => {
            setFiles([]);
            setTab('write');
          },
        })}
      </div>
    </div>
  );
}
