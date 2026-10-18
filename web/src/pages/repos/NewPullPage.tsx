import { ArrowLeft, GitBranch } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Button, ErrorAlert } from '../../components';
import { fetchBranches } from '../../lib/codeApi';
import { describeError } from '../../lib/http';
import { createPull } from '../../lib/pullsApi';
import type { Repository } from '../../lib/reposApi';
import { useLoad } from '../../lib/useLoad';

/** Nuova Pull Request da un confronto di branch (mockup 25/26, PR1): `?head=` e `?base=` precompilano la scelta. */
export function NewPullPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const branches = useLoad(() => fetchBranches(owner, name));
  const [base, setBase] = useState(params.get('base') ?? '');
  const [head, setHead] = useState(params.get('head') ?? '');
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [draft, setDraft] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const items = branches.data?.items ?? [];
  const target = base || items.find((b) => b.isDefault)?.name || repo.defaultBranch || '';
  const source = head;
  const same = source !== '' && source === target;
  const ready = title.trim() !== '' && source !== '' && !same && !busy;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const pr = await createPull(owner, name, { title: title.trim(), body, sourceBranch: source, targetBranch: target, draft });
      void navigate(`/${owner}/${name}/pulls/${pr.number}`);
    } catch (err) {
      setError(describeError(err));
      setBusy(false);
    }
  };

  return (
    <form className="stack form-stack" style={{ maxWidth: 820 }} onSubmit={(e) => void submit(e)}>
      <h1 style={{ fontSize: 22, fontWeight: 600, marginBottom: 4 }}>New pull request</h1>
      <p className="muted">Choose two branches to compare. The pull request proposes the changes of the compare branch for the base branch.</p>
      {repo.archived ? <p className="alert alert-warning" role="status">The repository is archived: pull requests can’t be opened.</p> : null}
      {branches.error ? <ErrorAlert message={branches.error} /> : null}
      <div className="row" style={{ alignItems: 'flex-end' }}>
        <label className="field" style={{ flex: 1 }}>
          <span>Base</span>
          <select className="select" aria-label="Base branch" value={target} onChange={(e) => setBase(e.target.value)}>
            {items.map((b) => (
              <option key={b.name} value={b.name}>
                {b.name}
              </option>
            ))}
          </select>
        </label>
        <ArrowLeft size={18} className="muted" aria-hidden="true" style={{ marginBottom: 10 }} />
        <label className="field" style={{ flex: 1 }}>
          <span>Compare</span>
          <select className="select" aria-label="Compare branch" value={source} onChange={(e) => setHead(e.target.value)}>
            <option value="">Choose a branch…</option>
            {items.map((b) => (
              <option key={b.name} value={b.name}>
                {b.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {same ? (
        <p className="alert alert-warning" role="status">
          Base and compare are the same branch: choose two different branches.
        </p>
      ) : source ? (
        <p className="small muted">
          <GitBranch size={14} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> <span className="branch-chip">{source}</span> into <span className="branch-chip">{target}</span>
        </p>
      ) : null}
      <label className="field">
        <span>Title</span>
        <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={256} />
      </label>
      <label className="field">
        <span>Description</span>
        <textarea className="textarea" value={body} onChange={(e) => setBody(e.target.value)} placeholder="Markdown supported. Use “Fixes #n” to close an issue on merge." />
      </label>
      <label className="check small">
        <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} />
        <span>Create as draft</span>
      </label>
      {error ? <ErrorAlert message={error} /> : null}
      <div className="row">
        <Button variant="primary" type="submit" disabled={!ready || repo.archived}>
          {draft ? 'Create draft pull request' : 'Create pull request'}
        </Button>
        <Link className="btn" to={`/${owner}/${name}/pulls`}>
          Cancel
        </Link>
      </div>
    </form>
  );
}
