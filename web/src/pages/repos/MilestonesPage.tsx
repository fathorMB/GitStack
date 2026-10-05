import { Plus } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, EmptyState, ErrorAlert, FormField, TextInput } from '../../components';
import { describeError, fieldErrors } from '../../lib/http';
import { addMilestone, editMilestone, fetchMilestones } from '../../lib/issuesApi';
import type { Milestone } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { useCanWrite } from '../../lib/useCanWrite';
import { useLoad } from '../../lib/useLoad';

// Avanzamento: closedIssues conta solo le chiuse come completed (I2).
function percent(m: Pick<Milestone, 'openIssues' | 'closedIssues'>): number {
  const total = m.openIssues + m.closedIssues;
  return total === 0 ? 0 : Math.round((m.closedIssues / total) * 100);
}

/** Milestone del repo (M-05/L, I7): aperte e chiuse; gestione solo con write. */
export function MilestonesPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const canWrite = useCanWrite(repo);
  const { data, loading, error, reload } = useLoad(() => fetchMilestones(owner, name));
  const [tab, setTab] = useState<'open' | 'closed'>('open');
  const [editing, setEditing] = useState<Milestone | 'new' | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function setState(m: Milestone, state: 'open' | 'closed') {
    setActionError(null);
    try {
      await editMilestone(owner, name, m.number, { state });
      await reload();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  if (loading && data === null) return <p className="muted">Loading milestones…</p>;
  if (error || !data) return <ErrorAlert message={error ?? 'Milestones not found.'} />;
  const open = data.items.filter((m) => m.state === 'open');
  const closed = data.items.filter((m) => m.state === 'closed');
  const shown = tab === 'open' ? open : closed;

  return (
    <section aria-label="Milestones">
      <div className="row">
        <h2 style={{ margin: 0 }}>Milestones</h2>
        <span className="seg" role="group" aria-label="Milestone state">
          <button type="button" className={tab === 'open' ? 'active' : undefined} aria-pressed={tab === 'open'} onClick={() => setTab('open')}>
            Open ({open.length})
          </button>
          <button type="button" className={tab === 'closed' ? 'active' : undefined} aria-pressed={tab === 'closed'} onClick={() => setTab('closed')}>
            Closed ({closed.length})
          </button>
        </span>
        <span className="sp" />
        {canWrite && editing === null ? (
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus size={14} aria-hidden="true" /> New milestone
          </Button>
        ) : null}
      </div>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {editing !== null ? (
        <MilestoneForm
          key={editing === 'new' ? 'new' : editing.number}
          milestone={editing === 'new' ? null : editing}
          onCancel={() => setEditing(null)}
          onSave={async (v) => {
            if (editing === 'new') await addMilestone(owner, name, v);
            else await editMilestone(owner, name, editing.number, v);
            setEditing(null);
            await reload();
          }}
        />
      ) : null}
      <div className="card section-gap">
        {shown.length === 0 ? (
          <EmptyState title={tab === 'open' ? 'No open milestones' : 'No closed milestones'} description="Milestones group issues toward a goal." />
        ) : (
          <ul className="list-plain" aria-label={`${tab} milestones`}>
            {shown.map((m) => {
              const pct = percent(m);
              return (
                <li key={m.number} className="stack card-b" style={{ borderBottom: '1px solid var(--border)' }}>
                  <div className="row">
                    <b>{m.title}</b>
                    <span className="muted small">{m.dueOn ? `Due ${m.dueOn}` : 'No due date'}</span>
                    <span className="sp" />
                    {canWrite ? (
                      <>
                        <Button size="sm" aria-label={`Edit milestone ${m.title}`} onClick={() => setEditing(m)}>
                          Edit
                        </Button>
                        <Button
                          size="sm"
                          aria-label={`${m.state === 'open' ? 'Close' : 'Reopen'} milestone ${m.title}`}
                          onClick={() => void setState(m, m.state === 'open' ? 'closed' : 'open')}
                        >
                          {m.state === 'open' ? 'Close' : 'Reopen'}
                        </Button>
                      </>
                    ) : null}
                  </div>
                  {m.description ? <p className="muted">{m.description}</p> : null}
                  <progress value={pct} max={100} aria-label={`${m.title} progress`} />
                  <span className="muted small">
                    {pct}% complete · {m.openIssues} open · {m.closedIssues} closed
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}

interface MilestoneValues {
  title: string;
  description: string;
  dueOn: string | null;
}

function MilestoneForm({ milestone, onSave, onCancel }: { milestone: Milestone | null; onSave: (v: MilestoneValues) => Promise<void>; onCancel: () => void }) {
  const [title, setTitle] = useState(milestone?.title ?? '');
  const [description, setDescription] = useState(milestone?.description ?? '');
  const [dueOn, setDueOn] = useState(milestone?.dueOn ?? '');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (title.trim() === '') {
      setFields({ title: 'Title is required.' });
      return;
    }
    setFields({});
    setSaving(true);
    setFormError(null);
    try {
      await onSave({ title: title.trim(), description: description.trim(), dueOn: dueOn === '' ? null : dueOn });
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
      setSaving(false);
    }
  }

  return (
    <form className="card card-b stack form-stack section-gap" aria-label={milestone ? 'Edit milestone' : 'New milestone'} noValidate onSubmit={(e) => void submit(e)}>
      {formError ? <ErrorAlert message={formError} /> : null}
      <FormField label="Milestone title" error={fields.title}>
        <TextInput value={title} maxLength={256} onChange={(e) => setTitle(e.target.value)} autoComplete="off" />
      </FormField>
      <FormField label="Due date (optional)" error={fields.dueOn}>
        <TextInput type="date" value={dueOn} onChange={(e) => setDueOn(e.target.value)} />
      </FormField>
      <FormField label="Description (optional)" error={fields.description}>
        <TextInput value={description} maxLength={4096} onChange={(e) => setDescription(e.target.value)} autoComplete="off" />
      </FormField>
      <div className="row">
        <span className="sp" />
        <Button onClick={onCancel}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={saving}>
          {saving ? 'Saving…' : milestone ? 'Save changes' : 'Create milestone'}
        </Button>
      </div>
    </form>
  );
}
