import { Inbox, Moon, Sun } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import {
  Button,
  Checkbox,
  ConfirmDialog,
  CopyButton,
  DataTable,
  EmptyState,
  FormField,
  PasswordInput,
  Select,
  StatusBadge,
  TextInput,
  useToast,
} from '../components';
import type { Status } from '../components';

// Pagina interna per la revisione contro lo styleguide: raggiungibile solo
// da /_components, non compare nella sidebar. Il pulsante in alto cambia
// tema (data-theme su <html>, come la styleguide).
type Theme = 'light' | 'dark';

function currentTheme(): Theme {
  const set = document.documentElement.getAttribute('data-theme');
  if (set === 'light' || set === 'dark') return set;
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

const ROWS = [
  { id: '1', name: 'deploy-key', kind: 'SSH key', status: 'open' as Status },
  { id: '2', name: 'ci-token', kind: 'Token', status: 'closed' as Status },
  { id: '3', name: 'build-agent', kind: 'Agent', status: 'agent' as Status },
];

export function ComponentsPage() {
  const { toast } = useToast();
  const [theme, setTheme] = useState<Theme>(currentTheme);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [name, setName] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const nameError = submitted && name.trim() === '' ? 'Name is required.' : undefined;

  function toggleTheme() {
    const next: Theme = theme === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    try {
      window.localStorage.setItem('gs-theme', next);
    } catch {
      // storage non disponibile: il tema vale per la sessione
    }
    setTheme(next);
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (name.trim() !== '') toast('Saved', 'success');
  }

  return (
    <>
      <div className="page-h">
        <h1>Components</h1>
        <span className="sp" />
        <Button onClick={toggleTheme} aria-pressed={theme === 'dark'}>
          {theme === 'dark' ? <Sun size={14} aria-hidden="true" /> : <Moon size={14} aria-hidden="true" />}
          {theme === 'dark' ? 'Light theme' : 'Dark theme'}
        </Button>
      </div>

      <section className="demo-sec" aria-labelledby="d-btn">
        <h2 id="d-btn">Buttons</h2>
        <div className="row">
          <Button>Default</Button>
          <Button variant="primary">Primary</Button>
          <Button variant="danger">Danger</Button>
          <Button variant="ghost">Ghost</Button>
          <Button size="sm">Small</Button>
          <Button size="lg">Large</Button>
          <Button disabled>Disabled</Button>
          <CopyButton value="git clone https://gitstack.example/org/repo.git" />
        </div>
      </section>

      <section className="demo-sec" aria-labelledby="d-form">
        <h2 id="d-form">Form</h2>
        <form className="demo-grid" onSubmit={onSubmit} noValidate>
          <FormField label="Name" hint="Shown in lists." error={nameError}>
            <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="my-token" />
          </FormField>
          <FormField label="Password">
            <PasswordInput autoComplete="new-password" />
          </FormField>
          <FormField label="Visibility">
            <Select
              placeholder="Choose…"
              options={[
                { value: 'private', label: 'Private' },
                { value: 'internal', label: 'Internal' },
                { value: 'public', label: 'Public' },
              ]}
            />
          </FormField>
          <Checkbox label="Initialize with a README" hint="Creates the first commit." />
          <div className="row">
            <Button type="submit" variant="primary">
              Save
            </Button>
          </div>
        </form>
      </section>

      <section className="demo-sec" aria-labelledby="d-badge">
        <h2 id="d-badge">Status badges</h2>
        <div className="row">
          {(['open', 'closed', 'agent', 'private', 'error'] as Status[]).map((s) => (
            <StatusBadge key={s} status={s} />
          ))}
        </div>
      </section>

      <section className="demo-sec" aria-labelledby="d-tbl">
        <h2 id="d-tbl">Table</h2>
        <DataTable
          caption="Credentials"
          rows={ROWS}
          rowKey={(r) => r.id}
          columns={[
            { key: 'name', header: 'Name', render: (r) => <b>{r.name}</b> },
            { key: 'kind', header: 'Type', render: (r) => r.kind },
            { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
          ]}
        />
      </section>

      <section className="demo-sec" aria-labelledby="d-empty">
        <h2 id="d-empty">Empty state</h2>
        <div className="card">
          <EmptyState
            icon={<Inbox size={32} aria-hidden="true" />}
            title="No tokens yet"
            description="Create a token to use the API from scripts and agents."
            action={<Button variant="primary">New token</Button>}
          />
        </div>
      </section>

      <section className="demo-sec" aria-labelledby="d-fb">
        <h2 id="d-fb">Feedback</h2>
        <div className="row">
          <Button variant="danger" onClick={() => setConfirmOpen(true)}>
            Delete…
          </Button>
          <Button onClick={() => toast('Something happened', 'info')}>Info toast</Button>
          <Button onClick={() => toast('Done', 'success')}>Success toast</Button>
          <Button onClick={() => toast('That failed', 'error')}>Error toast</Button>
        </div>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title="Delete this token?"
          description="Scripts using it will stop working. This cannot be undone."
          confirmLabel="Delete"
          destructive
          onConfirm={() => toast('Token deleted', 'success')}
        />
      </section>
    </>
  );
}
