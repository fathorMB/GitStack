import { Plus, Terminal } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { createSshKey, fetchSshKeys, removeSshKey } from '../../lib/accessApi';
import type { SshKey } from '../../lib/accessApi';
import { formatDate, usedText } from '../../lib/format';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

// Chiavi SSH (mockup 14 "SSH keys"): elenco con fingerprint, aggiunta
// (riga in formato authorized_keys) e cancellazione con conferma.
export function SshKeysPage() {
  const { data, setData, loading, error } = useLoad(fetchSshKeys);
  const { toast } = useToast();
  const keys = data ?? [];

  const [adding, setAdding] = useState(false);
  const [title, setTitle] = useState('');
  const [publicKey, setPublicKey] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [toDelete, setToDelete] = useState<SshKey | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  function resetForm() {
    setAdding(false);
    setTitle('');
    setPublicKey('');
    setFormError(null);
  }

  async function handleAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!title.trim() || !publicKey.trim()) {
      setFormError('Enter a title and paste the public key.');
      return;
    }
    setSubmitting(true);
    setFormError(null);
    try {
      const key = await createSshKey({ title: title.trim(), publicKey: publicKey.trim() });
      setData([key, ...keys]);
      toast(`SSH key "${key.title}" added.`, 'success');
      resetForm();
    } catch (err) {
      setFormError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(key: SshKey) {
    setActionError(null);
    try {
      await removeSshKey(key.id);
      setData(keys.filter((k) => k.id !== key.id));
      toast(`SSH key "${key.title}" deleted.`, 'success');
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <div>
      <div className="sec-h">
        <h2>SSH keys</h2>
        <span className="sp" />
        {!adding ? (
          <Button onClick={() => setAdding(true)}>
            <Plus size={16} aria-hidden="true" />
            Add SSH key
          </Button>
        ) : null}
      </div>

      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}

      {adding ? (
        <form className="card section-gap" onSubmit={(e) => void handleAdd(e)} noValidate aria-label="New SSH key">
          <div className="card-h">New SSH key</div>
          <div className="card-b stack form-stack">
            {formError ? <ErrorAlert message={formError} /> : null}
            <FormField label="Title">
              <TextInput value={title} onChange={(e) => setTitle(e.target.value)} placeholder="work-laptop" />
            </FormField>
            <FormField label="Public key" hint="Paste the contents of your .pub file, e.g. ssh-ed25519 AAAA… you@host.">
              <textarea
                className="input textarea mono"
                rows={4}
                value={publicKey}
                onChange={(e) => setPublicKey(e.target.value)}
                placeholder="ssh-ed25519 AAAA…"
              />
            </FormField>
            <div className="row">
              <span className="sp" />
              <Button onClick={resetForm}>Cancel</Button>
              <Button type="submit" variant="primary" disabled={submitting}>
                {submitting ? 'Adding…' : 'Add SSH key'}
              </Button>
            </div>
          </div>
        </form>
      ) : null}

      {loading && data === null ? (
        <p className="muted">Loading SSH keys…</p>
      ) : keys.length === 0 ? (
        error ? null : (
          <EmptyState
            icon={<Terminal size={32} />}
            title="No SSH keys"
            description="Add a public key to push and pull over SSH."
          />
        )
      ) : (
        <ul className="list" aria-label="SSH keys">
          {keys.map((k) => (
            <li key={k.id} className="list-row">
              <Terminal size={16} className="muted" aria-hidden="true" />
              <div className="grow">
                <b>{k.title}</b>
                <div className="mono small muted">
                  {k.fingerprint} · {k.keyType}
                </div>
              </div>
              <div className="small muted">
                Added {formatDate(k.createdAt)} · {usedText(k.lastUsedAt).toLowerCase()}
              </div>
              <Button size="sm" variant="danger" onClick={() => setToDelete(k)} aria-label={`Delete ${k.title}`}>
                Delete
              </Button>
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(open) => {
          if (!open) setToDelete(null);
        }}
        title="Delete SSH key?"
        description={`"${toDelete?.title ?? ''}" will no longer be able to authenticate over SSH.`}
        confirmLabel="Delete"
        destructive
        onConfirm={() => {
          if (toDelete) void handleDelete(toDelete);
        }}
      />
    </div>
  );
}
