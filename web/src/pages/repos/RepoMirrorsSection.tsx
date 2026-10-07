import { CircleAlert, CircleCheck, CircleX, Clock, GitFork, Loader } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, ErrorAlert, FormField, PasswordInput, TextInput, useToast } from '../../components';
import { formatDate, timeAgo } from '../../lib/format';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { addMirror, editMirror, fetchMirrorRuns, fetchMirrors, removeMirror, syncMirror } from '../../lib/mirrorsApi';
import type { RepoMirror, RepoMirrorRun, RepoMirrorRunOutcome, RepoMirrorState } from '../../lib/mirrorsApi';
import { useLoad } from '../../lib/useLoad';

// Stato = colore + icona + testo (mai solo colore).
const STATE: Record<RepoMirrorState, { label: string; color: string; Icon: typeof Clock }> = {
  in_sync: { label: 'In sync', color: 'var(--success)', Icon: CircleCheck },
  syncing: { label: 'Syncing', color: 'var(--info)', Icon: Loader },
  pending: { label: 'Queued', color: 'var(--info)', Icon: Clock },
  error: { label: 'Error', color: 'var(--danger)', Icon: CircleX },
  diverged: { label: 'Stopped: diverged', color: 'var(--warning)', Icon: CircleAlert },
};

const OUTCOME: Record<RepoMirrorRunOutcome, { label: string; color: string }> = {
  success: { label: 'Success', color: 'var(--success)' },
  error: { label: 'Error', color: 'var(--danger)' },
  diverged: { label: 'Diverged', color: 'var(--warning)' },
  blocked: { label: 'Blocked', color: 'var(--danger)' },
};

export function MirrorStateBadge({ state }: { state: RepoMirrorState }) {
  const { label, color, Icon } = STATE[state];
  return (
    <span className="badge" style={{ color, borderColor: color }} data-state={state}>
      <Icon size={13} aria-hidden="true" />
      {label}
    </span>
  );
}

function shortSha(sha: string): string {
  return sha.slice(0, 7);
}

function formError(err: unknown, field: string): string {
  if (err instanceof ApiError && (err.status === 409 || err.status === 422 || err.status === 400)) return fieldErrors(err)[field] ?? err.message;
  return describeError(err);
}

// Sezione Mirror delle impostazioni del repo (V8, GIT-180): stile della pagina
// Webhooks (mockup 11). Montata solo per chi ha admin.
export function RepoMirrorsSection({ owner, repo, readOnly }: { owner: string; repo: string; readOnly: boolean }) {
  const { toast } = useToast();
  const { data, setData, loading, error } = useLoad(() => fetchMirrors(owner, repo));
  const [form, setForm] = useState<'new' | RepoMirror | null>(null);
  const [confirm, setConfirm] = useState<RepoMirror | null>(null);
  const [actionError, setActionError] = useState('');
  const [busyId, setBusyId] = useState('');
  const [runsOf, setRunsOf] = useState('');

  const mirrors = data ?? [];

  function replace(updated: RepoMirror) {
    setData(mirrors.map((m) => (m.id === updated.id ? updated : m)));
  }

  async function act(m: RepoMirror, fn: () => Promise<RepoMirror | void>, message: string) {
    setBusyId(m.id);
    setActionError('');
    try {
      const updated = await fn();
      if (updated) replace(updated);
      toast(message, 'success');
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusyId('');
    }
  }

  async function remove(m: RepoMirror) {
    setBusyId(m.id);
    setActionError('');
    try {
      await removeMirror(owner, repo, m.id);
      setData(mirrors.filter((x) => x.id !== m.id));
      toast('Mirror deleted.', 'success');
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusyId('');
    }
  }

  return (
    <section aria-label="Mirrors">
      <div className="sec-h">
        <h2>Mirrors</h2>
        <span className="sp" />
        <Button variant="primary" disabled={readOnly || form !== null} onClick={() => setForm('new')}>
          Add mirror
        </Button>
      </div>
      <p className="muted" style={{ marginBottom: 16 }}>
        GitStack pushes the default branch and the tags to another Git server over HTTPS after every push. The push is never forced: if the destination
        has a different history the mirror stops and nothing is overwritten.
      </p>
      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {loading && data === null ? <p className="muted">Loading mirrors…</p> : null}
      {data !== null && mirrors.length === 0 && form === null ? (
        <p className="muted">
          <GitFork size={14} aria-hidden="true" /> No mirrors yet.
        </p>
      ) : null}
      {mirrors.length > 0 ? (
        <div className="list" style={{ marginBottom: 24 }}>
          {mirrors.map((m) => (
            <MirrorRow
              key={m.id}
              owner={owner}
              repo={repo}
              mirror={m}
              readOnly={readOnly}
              busy={busyId === m.id}
              showRuns={runsOf === m.id}
              onToggleRuns={() => setRunsOf(runsOf === m.id ? '' : m.id)}
              onSync={() => void act(m, () => syncMirror(owner, repo, m.id), 'Sync requested.')}
              onToggleEnabled={() =>
                void act(m, () => editMirror(owner, repo, m.id, { enabled: !m.enabled }), m.enabled ? 'Mirror disabled.' : 'Mirror enabled.')
              }
              onEdit={() => setForm(m)}
              onDelete={() => setConfirm(m)}
            />
          ))}
        </div>
      ) : null}
      {form !== null ? (
        <MirrorForm
          key={form === 'new' ? 'new' : form.id}
          owner={owner}
          repo={repo}
          mirror={form === 'new' ? null : form}
          onCancel={() => setForm(null)}
          onSaved={(saved, created) => {
            setData(created ? [...mirrors, saved] : mirrors.map((m) => (m.id === saved.id ? saved : m)));
            toast(created ? 'Mirror added.' : 'Mirror saved.', 'success');
            setForm(null);
          }}
        />
      ) : null}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(o) => !o && setConfirm(null)}
        title="Delete this mirror?"
        description={
          <>
            GitStack stops pushing to <span className="mono">{confirm?.url}</span>. The destination repository is left as it is.
          </>
        }
        confirmLabel="Delete mirror"
        destructive
        onConfirm={() => confirm && void remove(confirm)}
      />
    </section>
  );
}

function MirrorRow(props: {
  owner: string;
  repo: string;
  mirror: RepoMirror;
  readOnly: boolean;
  busy: boolean;
  showRuns: boolean;
  onToggleRuns: () => void;
  onSync: () => void;
  onToggleEnabled: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { mirror: m, readOnly, busy } = props;
  const pushed = Object.entries(m.lastPushed ?? {});
  return (
    <div data-testid="mirror-row">
      <div className="list-row" style={{ alignItems: 'flex-start' }}>
        <div style={{ flex: 1 }}>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
            <span className="mono" style={{ fontSize: 13 }}>
              {m.url}
            </span>
            <MirrorStateBadge state={m.state} />
            {!m.enabled ? <span className="badge">Disabled</span> : null}
          </div>
          <div className="small muted">
            user <span className="mono">{m.username}</span> ·{' '}
            {m.lastSuccessAt ? (
              <>
                last successful push {timeAgo(m.lastSuccessAt)} ({formatDate(m.lastSuccessAt)})
                {pushed.length > 0 ? (
                  <>
                    {' '}
                    {pushed.map(([ref, sha]) => (
                      <span key={ref} className="mono" title={`${ref} → ${sha}`}>
                        {' '}
                        {ref.replace(/^refs\/(heads|tags)\//, '')}@{shortSha(sha)}
                      </span>
                    ))}
                  </>
                ) : null}
              </>
            ) : (
              'never pushed'
            )}
          </div>
          {m.lastError ? (
            <div className="small" style={{ color: m.state === 'diverged' ? 'var(--warning)' : 'var(--danger)' }}>
              {m.lastError}
            </div>
          ) : null}
        </div>
        <Button size="sm" disabled={readOnly || busy || !m.enabled || m.state === 'syncing'} onClick={props.onSync}>
          Sync now
        </Button>
        <Button size="sm" disabled={readOnly || busy} onClick={props.onToggleEnabled}>
          {m.enabled ? 'Disable' : 'Enable'}
        </Button>
        <Button size="sm" disabled={readOnly || busy} onClick={props.onEdit} aria-label={`Edit mirror ${m.url}`}>
          Edit
        </Button>
        <Button size="sm" variant="danger" disabled={readOnly || busy} onClick={props.onDelete} aria-label={`Delete mirror ${m.url}`}>
          Delete
        </Button>
        <Button size="sm" aria-expanded={props.showRuns} onClick={props.onToggleRuns} aria-label={`Recent runs of ${m.url}`}>
          Runs
        </Button>
      </div>
      {props.showRuns ? <MirrorRuns key={m.lastAttemptAt ?? ''} owner={props.owner} repo={props.repo} mirror={m} /> : null}
    </div>
  );
}

function MirrorRuns({ owner, repo, mirror }: { owner: string; repo: string; mirror: RepoMirror }) {
  // Il chiamante lo rimonta (key) quando cambia l'ultimo tentativo.
  const { data, loading, error } = useLoad<RepoMirrorRun[]>(() => fetchMirrorRuns(owner, repo, mirror.id));
  return (
    <div className="card" style={{ margin: '0 16px 12px' }}>
      <div className="card-h">Recent runs</div>
      {error ? <ErrorAlert message={error} /> : null}
      {loading && data === null ? <p className="muted card-b">Loading runs…</p> : null}
      {data !== null && data.length === 0 ? <p className="muted card-b">No runs yet.</p> : null}
      {data !== null && data.length > 0 ? (
        <div className="list" style={{ border: 0, borderRadius: 0 }}>
          {data.map((r) => {
            const o = OUTCOME[r.outcome];
            return (
              <div key={r.id} className="list-row small" style={{ flexWrap: 'wrap' }}>
                <b style={{ color: o.color }}>{o.label}</b>
                <span className="muted">attempt {r.attempt}</span>
                <span className="muted">{r.durationMs} ms</span>
                <span className="sp" />
                <span className="subtle">{timeAgo(r.startedAt)}</span>
                {r.error ? <div style={{ flexBasis: '100%', color: 'var(--danger)' }}>{r.error}</div> : null}
                {r.refs.length > 0 ? (
                  <div className="mono muted" style={{ flexBasis: '100%' }}>
                    {r.refs.map((x) => (
                      <div key={x.ref}>
                        {x.ref} · {x.status}
                        {x.sha ? ` · ${shortSha(x.sha)}` : ''}
                        {x.reason ? ` · ${x.reason}` : ''}
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function MirrorForm(props: {
  owner: string;
  repo: string;
  mirror: RepoMirror | null;
  onCancel: () => void;
  onSaved: (m: RepoMirror, created: boolean) => void;
}) {
  const { owner, repo, mirror } = props;
  const [url, setUrl] = useState(mirror?.url ?? '');
  const [username, setUsername] = useState(mirror?.username ?? '');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [general, setGeneral] = useState('');

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErrors({});
    setGeneral('');
    try {
      if (mirror === null) {
        props.onSaved(await addMirror(owner, repo, { url: url.trim(), username: username.trim(), token }), true);
      } else {
        const input: { url?: string; username?: string; token?: string } = {};
        if (url.trim() !== mirror.url) input.url = url.trim();
        if (username.trim() !== mirror.username) input.username = username.trim();
        if (token !== '') input.token = token;
        props.onSaved(await editMirror(owner, repo, mirror.id, input), false);
      }
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.status === 422 || err.status === 400)) {
        const fields = fieldErrors(err);
        if (Object.keys(fields).length > 0) setErrors(fields);
        else setGeneral(formError(err, 'url'));
      } else {
        setGeneral(describeError(err));
      }
    } finally {
      setBusy(false);
    }
  }

  const valid = url.trim() !== '' && username.trim() !== '' && (mirror !== null || token !== '');
  return (
    <form className="card" onSubmit={(e) => void submit(e)} aria-label={mirror ? 'Edit mirror' : 'Add mirror'}>
      <div className="card-h">{mirror ? 'Edit mirror' : 'Add mirror'}</div>
      <div className="card-b stack form-stack" style={{ gap: 16 }}>
        {general ? <ErrorAlert message={general} /> : null}
        <FormField label="Remote URL" hint="An https:// URL, without credentials." error={errors.url}>
          <TextInput className="mono" value={url} placeholder="https://github.com/example/repo.git" onChange={(e) => setUrl(e.target.value)} />
        </FormField>
        <FormField label="Username" error={errors.username}>
          <TextInput value={username} autoComplete="off" onChange={(e) => setUsername(e.target.value)} />
        </FormField>
        <FormField
          label="Token"
          hint={mirror ? 'A token is saved and never shown again. Leave empty to keep it, or enter a new one to replace it.' : 'Stored encrypted; it is never shown again.'}
          error={errors.token}
        >
          <PasswordInput value={token} autoComplete="new-password" onChange={(e) => setToken(e.target.value)} />
        </FormField>
      </div>
      <div className="card-b row" style={{ borderTop: '1px solid var(--border)' }}>
        <span className="sp" />
        <Button type="button" onClick={props.onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" disabled={busy || !valid}>
          {mirror ? 'Save mirror' : 'Add mirror'}
        </Button>
      </div>
    </form>
  );
}
