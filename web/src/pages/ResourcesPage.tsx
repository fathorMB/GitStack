import { AlertTriangle, Boxes, Plus } from 'lucide-react';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import type { Resource } from '@gitstack/api-client';
import { ApiError, createTestResource, fetchResources } from '../lib/resourcesApi';

// Pagina di prova end-to-end di M-01 (T-07): elenca e crea la risorsa
// generica (D15, [c_c1087c1c051de3d0]) passando solo dal gateway, con il
// client TS generato da T-03 (client/ts). Non è ancora una funzionalità di
// prodotto (repository, issue...): serve a dimostrare che UI, gateway e
// core sono collegati end-to-end (M-01, [c_8289c650b4118599]).
export function ResourcesPage() {
  const [resources, setResources] = useState<Resource[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [type, setType] = useState('demo');
  const [name, setName] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await fetchResources();
      setResources(list.items);
    } catch (err) {
      setError(describeError(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim() || !type.trim()) {
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const created = await createTestResource({ type: type.trim(), name: name.trim() });
      setResources((prev) => [created, ...prev]);
      setName('');
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <div className="page-h">
        <h1>Resources</h1>
        <span className="counter">{resources.length}</span>
      </div>
      <p className="muted" style={{ margin: '0 0 20px', maxWidth: 640 }}>
        Verifica end-to-end di M-01: elenca e crea la risorsa generica tramite il gateway, usando solo il client
        TypeScript generato dal contratto OpenAPI.
      </p>

      {error ? (
        <div className="alert alert-danger" style={{ marginBottom: 16 }} role="alert">
          <AlertTriangle size={16} strokeWidth={1.8} />
          <div>{error}</div>
        </div>
      ) : null}

      <form className="card card-b stack" style={{ gap: 12, marginBottom: 20, maxWidth: 480 }} onSubmit={handleSubmit}>
        <div className="row" style={{ gap: 12, alignItems: 'flex-end' }}>
          <div className="field" style={{ width: 140 }}>
            <label htmlFor="resource-type">Type</label>
            <input
              id="resource-type"
              className="input"
              value={type}
              onChange={(event) => setType(event.target.value)}
              required
            />
          </div>
          <div className="field" style={{ flex: 1 }}>
            <label htmlFor="resource-name">Name</label>
            <input
              id="resource-name"
              className="input"
              placeholder="What is this resource for?"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
            />
          </div>
          <button type="submit" className="btn btn-primary" disabled={submitting}>
            <Plus size={16} strokeWidth={1.8} />
            Create resource
          </button>
        </div>
      </form>

      {loading ? (
        <p className="muted">Loading…</p>
      ) : resources.length === 0 ? (
        <div className="card empty">
          <Boxes size={32} strokeWidth={1.5} />
          <h4>No resources yet</h4>
          <p>Create the first test resource with the form above.</p>
        </div>
      ) : (
        <div className="list">
          <div className="list-row list-head">
            <span style={{ flex: 1 }}>Name</span>
            <span style={{ width: 120 }}>Type</span>
          </div>
          {resources.map((resource) => (
            <div className="list-row" key={resource.id}>
              <span style={{ flex: 1, fontWeight: 600 }}>{resource.name}</span>
              <span className="badge mono" style={{ width: 'fit-content' }}>
                {resource.type}
              </span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}

function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.message} (${err.code})`;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return 'Errore imprevisto nel parlare con il gateway.';
}
