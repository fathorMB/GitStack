import { TriangleAlert } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { CSSProperties } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, ErrorAlert } from '../../components';
import { describeError } from '../../lib/http';
import { createNewIssue, fetchLabels, fetchMilestones, fetchMyRole, fetchTemplates } from '../../lib/issuesApi';
import type { IssueTemplate, Label, Milestone, Role } from '../../lib/issuesApi';
import { defaultMentionSource } from '../../lib/mentions';
import type { MentionSource } from '../../lib/mentions';
import type { Repository } from '../../lib/reposApi';
import { IssueEditor } from './IssueEditor';
import { loadMe } from './repoAdmin';

interface Data {
  templates: IssueTemplate[];
  write: boolean;
  me: string;
  labels: Label[];
  milestones: Milestone[];
}

const BLANK = '';

/** Nuova issue (mockup 20; I3, I8, I9, I11). Chi ha read apre la issue senza assegnatari, etichette e milestone. */
export function NewIssuePage({ repo, mentionSource }: { repo: Repository; mentionSource?: MentionSource }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const [data, setData] = useState<Data | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const [templates, role, me] = await Promise.all([
          fetchTemplates(owner, name).catch(() => [] as IssueTemplate[]),
          fetchMyRole(repo.id).catch(() => null as Role),
          loadMe().catch(() => null),
        ]);
        const write = role === 'write' || role === 'admin' || me?.isAdmin === true;
        const [labels, milestones] = write
          ? await Promise.all([fetchLabels(owner, name).then((l) => l.items, () => [] as Label[]), fetchMilestones(owner, name).then((m) => m.items.filter((x) => x.state === 'open'), () => [] as Milestone[])])
          : [[] as Label[], [] as Milestone[]];
        if (live) setData({ templates, write, me: me?.username ?? '', labels, milestones });
      } catch (err) {
        if (live) setLoadError(describeError(err));
      }
    })();
    return () => {
      live = false;
    };
  }, [owner, name, repo.id]);

  return (
    <div>
      <h1 style={{ fontSize: 22, fontWeight: 600, marginBottom: 4 }}>New issue</h1>
      {repo.archived ? (
        <div className="alert alert-warning" role="status">
          <TriangleAlert size={16} aria-hidden="true" />
          <span>This repository is archived: new issues cannot be opened.</span>
        </div>
      ) : loadError ? (
        <ErrorAlert message={loadError} />
      ) : !data ? (
        <p className="muted">Loading…</p>
      ) : (
        <Form repo={repo} data={data} mentionSource={mentionSource} />
      )}
    </div>
  );
}

function Form({ repo, data, mentionSource }: { repo: Repository; data: Data; mentionSource?: MentionSource }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const navigate = useNavigate();
  const { templates, write } = data;
  const first = templates[0];
  const [tpl, setTpl] = useState<string>(first ? first.name : BLANK);
  const [title, setTitle] = useState(first?.title ?? '');
  const [body, setBody] = useState(first?.body ?? '');
  const [labels, setLabels] = useState<string[]>(first?.labels ?? []);
  const [assignees, setAssignees] = useState<string[]>([]);
  const [assignee, setAssignee] = useState('');
  const [milestone, setMilestone] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const mentions = useMemo(() => mentionSource ?? defaultMentionSource(repo.owner), [mentionSource, repo.owner]);

  const choose = (t: IssueTemplate | null) => {
    const prev = templates.find((x) => x.name === tpl);
    setTpl(t ? t.name : BLANK);
    setTitle(t?.title ?? '');
    setBody(t?.body ?? '');
    setLabels((cur) => {
      const base = cur.filter((l) => !(prev?.labels ?? []).includes(l));
      return [...base, ...(t?.labels ?? []).filter((l) => !base.includes(l))];
    });
  };

  const submit = async (attachmentIds: string[]) => {
    if (!title.trim() || busy) return;
    setBusy(true);
    setError(null);
    try {
      const issue = await createNewIssue(owner, name, {
        title: title.trim(),
        ...(body.trim() ? { body } : {}),
        ...(attachmentIds.length ? { attachmentIds } : {}),
        ...(write && labels.length ? { labels } : {}),
        ...(write && assignees.length ? { assignees } : {}),
        ...(write && milestone ? { milestone: Number(milestone) } : {}),
      });
      navigate(`/${owner}/${name}/issues/${issue.number}`);
    } catch (err) {
      setError(describeError(err));
      setBusy(false);
    }
  };

  const toggleLabel = (n: string, on: boolean) => setLabels((cur) => (on ? [...cur, n] : cur.filter((x) => x !== n)));
  const addAssignee = (u: string) => {
    const v = u.trim();
    if (v && !assignees.includes(v)) setAssignees((cur) => [...cur, v]);
  };
  const color = (n: string) => data.labels.find((l) => l.name === n)?.color;

  return (
    <>
      <p className="muted" style={{ marginBottom: 16 }}>
        {templates.length > 0 ? (
          <>
            Pick a template. Templates live in the repository under <span className="mono">.gitstack/ISSUE_TEMPLATE/</span>.
          </>
        ) : (
          'Describe the problem or the idea.'
        )}
      </p>
      {templates.length > 0 ? (
        <div className="scope-grid" role="radiogroup" aria-label="Issue template" style={{ marginBottom: 22 }}>
          {[...templates, null].map((t) => {
            const id = t ? t.name : BLANK;
            const on = tpl === id;
            return (
              <label key={id || 'blank'} className="check scope" style={on ? { borderColor: 'var(--accent)', background: 'var(--accent-soft)' } : undefined}>
                <input type="radio" name="tpl" checked={on} onChange={() => choose(t)} />
                <span>
                  <b>{t ? t.name : 'Blank issue'}</b>
                  <br />
                  <span className="small muted">
                    {t ? (t.about ?? '') : 'Start from an empty description.'}
                    {t && write && t.labels && t.labels.length > 0 ? (
                      <>
                        {' '}
                        Adds{' '}
                        {t.labels.map((l) => (
                          <span key={l} className="label" style={{ '--lc': `#${color(l) ?? '8b949e'}`, marginLeft: 4 } as CSSProperties}>
                            {l}
                          </span>
                        ))}
                      </>
                    ) : null}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      ) : null}

      <div className="issue-grid">
        <div className="stack" style={{ gap: 14 }}>
          <div className="field">
            <label htmlFor="new-issue-title">Title</label>
            <input id="new-issue-title" className="input" value={title} maxLength={256} onChange={(e) => setTitle(e.target.value)} />
          </div>
          <IssueEditor
            owner={owner}
            repo={name}
            label="Description"
            value={body}
            onChange={setBody}
            mentions={mentions}
            minHeight={220}
            actions={({ attachmentIds }) => (
              <>
                <Link className="btn" to={`/${owner}/${name}/issues`}>
                  Cancel
                </Link>
                <Button variant="primary" disabled={!title.trim() || busy} onClick={() => void submit(attachmentIds)}>
                  Submit new issue
                </Button>
              </>
            )}
          />
          <span className="small muted">Only people who can see this repository can open attachments.</span>
          {error ? <ErrorAlert message={error} /> : null}
        </div>

        <aside aria-label="Issue properties">
          {write ? (
            <>
              <div className="side-sec">
                <h4>Assignees</h4>
                {assignees.length === 0 ? <div className="small muted">No one</div> : null}
                {assignees.map((a) => (
                  <div key={a} className="row" style={{ marginBottom: 4 }}>
                    <b>{a}</b>
                    <Button variant="ghost" size="sm" aria-label={`Unassign ${a}`} onClick={() => setAssignees((cur) => cur.filter((x) => x !== a))}>
                      Remove
                    </Button>
                  </div>
                ))}
                <form
                  className="row"
                  style={{ flexWrap: 'nowrap', marginTop: 6 }}
                  onSubmit={(e) => {
                    e.preventDefault();
                    addAssignee(assignee);
                    setAssignee('');
                  }}
                >
                  <input className="input" aria-label="Assignee username" placeholder="username (needs Write)" value={assignee} onChange={(e) => setAssignee(e.target.value)} />
                  <Button type="submit" size="sm" disabled={!assignee.trim()}>
                    Assign
                  </Button>
                </form>
                {data.me && !assignees.includes(data.me) ? (
                  <Button variant="ghost" size="sm" onClick={() => addAssignee(data.me)}>
                    Assign yourself
                  </Button>
                ) : null}
              </div>
              <div className="side-sec">
                <h4>Labels</h4>
                {labels.length === 0 ? <span className="small muted">None yet</span> : null}
                <div className="stack" style={{ gap: 4 }} role="group" aria-label="Labels of this issue">
                  {data.labels.map((l) => (
                    <label key={l.id} className="check">
                      <input type="checkbox" checked={labels.includes(l.name)} onChange={(e) => toggleLabel(l.name, e.target.checked)} />
                      <span className="label" style={{ '--lc': `#${l.color}` } as CSSProperties}>
                        {l.name}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
              <div className="side-sec">
                <h4>Milestone</h4>
                <select className="select" aria-label="Milestone" value={milestone} onChange={(e) => setMilestone(e.target.value)}>
                  <option value="">No milestone</option>
                  {data.milestones.map((m) => (
                    <option key={m.number} value={String(m.number)}>
                      {m.title}
                    </option>
                  ))}
                </select>
              </div>
            </>
          ) : null}
          <div className="side-sec" style={{ border: 0 }}>
            <p className="small muted">
              Assignees, labels and milestone can be set by people with <b>Write</b> access. Anyone who can see the repository can open an issue.
            </p>
          </div>
        </aside>
      </div>
    </>
  );
}
