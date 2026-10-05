import { Pencil, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, FormField, TextInput } from '../../components';
import { describeError, fieldErrors } from '../../lib/http';
import { addLabel, editLabel, fetchLabels, removeLabel } from '../../lib/issuesApi';
import type { Label } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { useCanWrite } from '../../lib/useCanWrite';
import { useLoad } from '../../lib/useLoad';

const COLOR_RE = /^[0-9a-fA-F]{6}$/;

/** Etichette del repo (M-05/L, I5): elenco per tutti, gestione solo con write. */
export function LabelsPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const canWrite = useCanWrite(repo);
  const { data, loading, error, reload } = useLoad(() => fetchLabels(owner, name));
  const [editing, setEditing] = useState<Label | 'new' | null>(null);
  const [toDelete, setToDelete] = useState<Label | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function confirmDelete() {
    if (!toDelete) return;
    setActionError(null);
    try {
      await removeLabel(owner, name, toDelete.name);
      await reload();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  if (loading && data === null) return <p className="muted">Loading labels…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Labels not found.'} />;
  const items = data.items;

  return (
    <section aria-label="Labels">
      <div className="row">
        <h2 style={{ margin: 0 }}>Labels</h2>
        <span className="counter">{data.total}</span>
        <span className="sp" />
        {canWrite && editing === null ? (
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus size={14} aria-hidden="true" /> New label
          </Button>
        ) : null}
      </div>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {editing !== null ? (
        <LabelForm
          key={editing === 'new' ? 'new' : editing.id}
          label={editing === 'new' ? null : editing}
          onCancel={() => setEditing(null)}
          onSave={async (v) => {
            if (editing === 'new') await addLabel(owner, name, v);
            else await editLabel(owner, name, editing.name, v);
            setEditing(null);
            await reload();
          }}
        />
      ) : null}
      <div className="card section-gap">
        {items.length === 0 ? (
          <EmptyState title="No labels yet" description={canWrite ? 'Create the first label.' : 'This repository has no labels.'} />
        ) : (
          <ul className="list-plain" aria-label="Label list">
            {items.map((l) => (
              <li key={l.id} className="row card-b" style={{ borderBottom: '1px solid var(--border)' }}>
                <span className="badge" style={{ background: `#${l.color}`, color: '#fff', borderColor: `#${l.color}` }}>
                  {l.name}
                </span>
                <span className="muted grow">{l.description}</span>
                <span className="muted small">{l.openIssues} open</span>
                {canWrite ? (
                  <>
                    <Button size="sm" aria-label={`Edit label ${l.name}`} onClick={() => setEditing(l)}>
                      <Pencil size={14} aria-hidden="true" /> Edit
                    </Button>
                    <Button size="sm" variant="danger" aria-label={`Delete label ${l.name}`} onClick={() => setToDelete(l)}>
                      <Trash2 size={14} aria-hidden="true" /> Delete
                    </Button>
                  </>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => {
          if (!o) setToDelete(null);
        }}
        title="Delete label"
        description={`Delete "${toDelete?.name ?? ''}"? It is removed from every issue that uses it.`}
        confirmLabel="Delete label"
        destructive
        onConfirm={() => void confirmDelete()}
      />
    </section>
  );
}

interface LabelValues {
  name: string;
  color: string;
  description: string;
}

function LabelForm({ label, onSave, onCancel }: { label: Label | null; onSave: (v: LabelValues) => Promise<void>; onCancel: () => void }) {
  const [name, setName] = useState(label?.name ?? '');
  const [color, setColor] = useState(label?.color ?? '0969da');
  const [description, setDescription] = useState(label?.description ?? '');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const local: Record<string, string> = {};
    if (name.trim() === '') local.name = 'Name is required.';
    if (!COLOR_RE.test(color)) local.color = 'Use six hex digits, without #.';
    setFields(local);
    if (Object.keys(local).length > 0) return;
    setSaving(true);
    setFormError(null);
    try {
      await onSave({ name: name.trim(), color: color.toLowerCase(), description: description.trim() });
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
      setSaving(false);
    }
  }

  return (
    <form className="card card-b stack form-stack section-gap" aria-label={label ? 'Edit label' : 'New label'} noValidate onSubmit={(e) => void submit(e)}>
      {formError ? <ErrorAlert message={formError} /> : null}
      <FormField label="Label name" error={fields.name}>
        <TextInput value={name} maxLength={50} onChange={(e) => setName(e.target.value)} autoComplete="off" />
      </FormField>
      <FormField label="Color" hint="Six hex digits, e.g. d73a4a." error={fields.color}>
        <TextInput className="input mono" value={color} maxLength={6} onChange={(e) => setColor(e.target.value.replace('#', ''))} autoComplete="off" />
      </FormField>
      <FormField label="Description (optional)" error={fields.description}>
        <TextInput value={description} maxLength={256} onChange={(e) => setDescription(e.target.value)} autoComplete="off" />
      </FormField>
      <div className="row">
        <span className="badge" style={COLOR_RE.test(color) ? { background: `#${color}`, color: '#fff', borderColor: `#${color}` } : undefined}>
          {name || 'Preview'}
        </span>
        <span className="sp" />
        <Button onClick={onCancel}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={saving}>
          {saving ? 'Saving…' : label ? 'Save changes' : 'Create label'}
        </Button>
      </div>
    </form>
  );
}
