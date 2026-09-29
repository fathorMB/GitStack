import { Building2, Plus } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, EmptyState, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { describeError, fieldErrors } from '../../lib/http';
import { createOrg, fetchOrganizations } from '../../lib/orgsApi';
import { useLoad } from '../../lib/useLoad';

// Elenco delle organizzazioni dell'utente e creazione di una nuova
// (ingresso al mockup 15). Il creatore diventa owner: lo decide il backend.
export function OrgsPage() {
  const { data, loading, error } = useLoad(fetchOrganizations);
  const { toast } = useToast();
  const navigate = useNavigate();
  const orgs = data ?? [];

  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  function resetForm() {
    setAdding(false);
    setName('');
    setDisplayName('');
    setDescription('');
    setFormError(null);
    setFields({});
  }

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
      setFields({ name: 'Enter a name.' });
      return;
    }
    setSubmitting(true);
    setFormError(null);
    setFields({});
    try {
      const org = await createOrg({
        name: name.trim(),
        ...(displayName.trim() ? { displayName: displayName.trim() } : {}),
        ...(description.trim() ? { description: description.trim() } : {}),
      });
      toast(`Organization "${org.name}" created.`, 'success');
      void navigate(`/orgs/${encodeURIComponent(org.name)}`);
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div>
      <div className="page-h">
        <h1>Organizations</h1>
        <span className="sp" />
        {!adding ? (
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={16} aria-hidden="true" />
            New organization
          </Button>
        ) : null}
      </div>

      {error ? <ErrorAlert message={error} /> : null}

      {adding ? (
        <form className="card section-gap" onSubmit={(e) => void handleCreate(e)} noValidate aria-label="New organization">
          <div className="card-h">New organization</div>
          <div className="card-b stack form-stack">
            {formError ? <ErrorAlert message={formError} /> : null}
            <FormField label="Name" hint="Lowercase letters, digits and dashes: it appears in URLs." error={fields.name}>
              <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="acme" />
            </FormField>
            <FormField label="Display name" error={fields.displayName}>
              <TextInput value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="Acme Inc." />
            </FormField>
            <FormField label="Description" error={fields.description}>
              <TextInput value={description} onChange={(e) => setDescription(e.target.value)} />
            </FormField>
            <div className="row">
              <span className="sp" />
              <Button onClick={resetForm}>Cancel</Button>
              <Button type="submit" variant="primary" disabled={submitting}>
                {submitting ? 'Creating…' : 'Create organization'}
              </Button>
            </div>
          </div>
        </form>
      ) : null}

      {loading && data === null ? (
        <p className="muted">Loading organizations…</p>
      ) : orgs.length === 0 ? (
        error ? null : (
          <EmptyState
            icon={<Building2 size={32} />}
            title="No organizations"
            description="Create an organization to group members and teams."
          />
        )
      ) : (
        <ul className="list list-clean" aria-label="Organizations">
          {orgs.map((o) => (
            <li key={o.id} className="list-row">
              <span className="avatar avatar-lg org-avatar" aria-hidden="true">
                {(o.displayName || o.name).slice(0, 2).toUpperCase()}
              </span>
              <div className="grow">
                <Link to={`/orgs/${encodeURIComponent(o.name)}`}>
                  <b>{o.displayName || o.name}</b>
                </Link>
                <div className="small muted">{o.description || o.name}</div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
