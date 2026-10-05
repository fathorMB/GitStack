import { Info, Paperclip, X } from 'lucide-react';
import { useRef, useState } from 'react';
import type { ChangeEvent, ClipboardEvent, DragEvent, ReactNode } from 'react';
import { Button, ErrorAlert, Markdown } from '../../components';
import { formatBytes } from '../../lib/format';
import { describeError } from '../../lib/http';
import { MAX_ATTACHMENT_BYTES, uploadAttachment } from '../../lib/issuesApi';
import type { IssueAttachment } from '../../lib/issuesApi';

export interface IssueEditorProps {
  owner: string;
  repo: string;
  /** Etichetta accessibile della textarea. */
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  /** Con false niente allegati (es. modifica di un commento: il contratto non li accetta). */
  attachments?: boolean;
  /** Pulsanti a destra del footer; ricevono gli id degli allegati caricati. */
  actions: (ctx: { attachmentIds: string[]; clear: () => void }) => ReactNode;
}

/**
 * Editor Write/Preview (mockup 13): anteprima con il componente Markdown,
 * allegati fino a 10 MB (I9) caricati a parte e collegati con attachmentIds.
 */
export function IssueEditor({ owner, repo, label, value, onChange, placeholder, attachments = true, actions }: IssueEditorProps) {
  const [tab, setTab] = useState<'write' | 'preview'>('write');
  const [files, setFiles] = useState<IssueAttachment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);

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
        <textarea className="textarea" aria-label={label} placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} onPaste={onPaste} />
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
