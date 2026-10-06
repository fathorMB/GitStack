import { Archive, Bot, Check, ChevronDown, CircleCheck, CircleDot, CircleSlash, Copy, Eye, EyeOff, File as FileIcon, Flag, Lock, LockOpen, Plus, Settings, Tag as TagIcon, User as UserIcon, X } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Button, ConfirmDialog, ErrorAlert, Markdown } from '../../components';
import { formatBytes, timeAgo } from '../../lib/format';
import { describeError } from '../../lib/http';
import {
  closeIssueWith,
  downloadAttachment,
  editComment,
  editIssue,
  fetchCommentVersions,
  fetchIssue,
  fetchIssueComments,
  fetchIssueEvents,
  fetchIssueVersions,
  fetchLabels,
  fetchMilestones,
  fetchMyRole,
  postComment,
  putAssignees,
  putLabels,
  putMilestone,
  removeComment,
  reopen,
  setHidden,
  setLocked,
} from '../../lib/issuesApi';
import type { Issue, IssueAttachment, IssueCloseReason, IssueComment, IssueEvent, IssueUser, IssueViaToken, Label, Milestone, Role, TextVersion } from '../../lib/issuesApi';
import type { Repository } from '../../lib/reposApi';
import { permissions } from '../../lib/issuePerms';
import { IssueEditor } from './IssueEditor';
import { groupCommitEvents, linkedCommits } from '../../lib/issueLinks';
import { ClosedByCommitEvent, LinkedCommitsBox, LinkedCommitsEvent, ReferencedFromEvent } from './IssueLinks';
import { IssueSubscriptionBox } from './IssueSubscriptionBox';
import { loadMe } from './repoAdmin';

interface Data {
  issue: Issue;
  events: IssueEvent[];
  comments: IssueComment[];
  role: Role;
  me: string;
  isSystemAdmin: boolean;
  labels: Label[];
  milestones: Milestone[];
}

const REASON_TEXT: Record<string, string> = { completed: 'completed', not_planned: 'not planned', duplicate: 'duplicate' };

/** Dettaglio issue (mockup 13, I2, I3, I4, I6, I7, I11). Senza le parti di M-06. */
export function IssueDetailPage({ repo }: { repo: Repository }) {
  const { number: raw = '' } = useParams();
  const number = Number(raw);
  const owner = repo.owner.name;
  const name = repo.name;
  const [data, setData] = useState<Data | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const ref = { owner, repo: name, number };
    try {
      const [issue, events, comments, role, me, labels, milestones] = await Promise.all([
        fetchIssue(ref),
        fetchIssueEvents(ref),
        fetchIssueComments(ref),
        fetchMyRole(repo.id).catch(() => null as Role),
        loadMe().catch(() => null),
        fetchLabels(owner, name).then((l) => l.items, () => [] as Label[]),
        fetchMilestones(owner, name).then((m) => m.items, () => [] as Milestone[]),
      ]);
      setData({ issue, events: events.items, comments: comments.items, role, me: me?.username ?? '', isSystemAdmin: me?.isAdmin === true, labels, milestones });
      setLoadError(null);
    } catch (err) {
      setLoadError(describeError(err));
    }
  }, [owner, name, number, repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

  /** Esegue un'azione, poi ricarica: l'errore resta visibile e non perde la pagina. */
  const act = useCallback(
    async (fn: () => Promise<unknown>) => {
      setActionError(null);
      try {
        await fn();
        await load();
        return true;
      } catch (err) {
        setActionError(describeError(err));
        return false;
      }
    },
    [load],
  );

  if (!Number.isInteger(number) || number < 1) return <ErrorAlert message="Issue not found." />;
  if (loadError && !data) return <ErrorAlert message={loadError} />;
  if (!data) return <p className="muted">Loading issue…</p>;

  return <Detail repo={repo} data={data} act={act} actionError={actionError} number={number} />;
}

function Avatar({ user }: { user: IssueUser }) {
  return user.kind === 'agent' ? (
    <span className="avatar avatar-lg av-bot" aria-hidden="true">
      <Bot size={20} />
    </span>
  ) : (
    <span className="avatar avatar-lg" aria-hidden="true">
      {user.username.slice(0, 2).toUpperCase()}
    </span>
  );
}

function AgentBadge({ user }: { user: IssueUser }) {
  return user.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null;
}

// ViaToken: «via token <nome>» accanto all'autore (mockup 13); niente per le sessioni.
function ViaToken({ via }: { via?: IssueViaToken }) {
  if (!via) return null;
  return (
    <span className="muted" data-testid="via-token">
      via token <span className="mono">{via.name}</span>
    </span>
  );
}

function StateBadge({ issue }: { issue: Issue }) {
  if (issue.state === 'open') {
    return (
      <span className="badge badge-open">
        <CircleDot size={14} aria-hidden="true" /> Open
      </span>
    );
  }
  const r = issue.closeReason ?? 'completed';
  const text = r === 'duplicate' && issue.duplicateOf ? `Closed as duplicate of #${issue.duplicateOf}` : `Closed as ${REASON_TEXT[r] ?? r}`;
  return (
    <span className="badge badge-closed">
      {r === 'completed' ? <CircleCheck size={14} aria-hidden="true" /> : <CircleSlash size={14} aria-hidden="true" />} {text}
    </span>
  );
}

type Act = (fn: () => Promise<unknown>) => Promise<boolean>;

function Detail({ repo, data, act, actionError, number }: { repo: Repository; data: Data; act: Act; actionError: string | null; number: number }) {
  const { issue, events, comments, role, me, isSystemAdmin } = data;
  const owner = repo.owner.name;
  const name = repo.name;
  const ref = useMemo(() => ({ owner, repo: name, number }), [owner, name, number]);
  const perm = permissions(issue, role, me, repo.archived, isSystemAdmin);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(issue.title);
  const [body, setBody] = useState(issue.body);

  const hiddenEvent = useMemo(
    () => events.filter((e) => e.type === 'hidden').sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))[0],
    [events],
  );

  const timeline = useMemo(() => {
    type Item = { at: string; node: ReactNode };
    const items: Item[] = [];
    for (const c of comments) {
      items.push({ at: c.createdAt, node: <CommentView key={`c-${c.id}`} c={c} issue={issue} perm={perm} me={me} ref0={ref} act={act} /> });
    }
    for (const e of events) {
      // «opened» e' l'intestazione, «edited» e' il flag; i commenti eliminati sono nella cronologia dei commenti (M-06 per i riferimenti).
      if (e.type === 'opened' || e.type === 'edited' || e.type === 'referenced' || e.type === 'comment_deleted') continue;
      if (e.type === 'commit_linked') continue; // raggruppati per push qui sotto
      if (e.type === 'referenced_from') {
        items.push({ at: e.createdAt, node: <ReferencedFromEvent key={`e-${e.id}`} e={e} /> });
      } else if (e.type === 'closed_by_commit') {
        items.push({ at: e.createdAt, node: <ClosedByCommitEvent key={`e-${e.id}`} e={e} repoFullName={`${owner}/${name}`} defaultBranch={repo.defaultBranch} /> });
      } else {
        items.push({ at: e.createdAt, node: <EventView key={`e-${e.id}`} e={e} /> });
      }
    }
    for (const g of groupCommitEvents(events)) {
      items.push({ at: g.at, node: <LinkedCommitsEvent key={`g-${g.id}`} group={g} repoFullName={`${owner}/${name}`} /> });
    }
    return items.sort((a, b) => Date.parse(a.at) - Date.parse(b.at)).map((i) => i.node);
  }, [comments, events, issue, perm, me, ref, act, owner, name, repo.defaultBranch]);

  const saveEdit = async () => {
    if (!title.trim()) return;
    if (await act(() => editIssue(ref, { title: title.trim(), body }))) setEditing(false);
  };

  return (
    <div>
      <div className="row" style={{ marginBottom: 8, flexWrap: 'nowrap', alignItems: 'flex-start' }}>
        {editing ? (
          <input className="input" style={{ flex: 1 }} aria-label="Title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={256} />
        ) : (
          <h2 className="issue-title" style={{ flex: 1 }}>
            {issue.title} <span>#{issue.number}</span>
          </h2>
        )}
        {perm.canEdit && !editing ? (
          <Button onClick={() => { setTitle(issue.title); setBody(issue.body); setEditing(true); }}>Edit</Button>
        ) : null}
        {repo.archived ? null : (
          <Link className="btn btn-primary" to={`/${owner}/${name}/issues/new`}>
            <Plus size={16} aria-hidden="true" /> New issue
          </Link>
        )}
      </div>
      <div className="row" style={{ paddingBottom: 16, borderBottom: '1px solid var(--border)', marginBottom: 22 }}>
        <StateBadge issue={issue} />
        <span className="muted">
          <b style={{ color: 'var(--text)' }}>{issue.author.username}</b> opened this issue {timeAgo(issue.createdAt)} · {issue.commentCount} comment{issue.commentCount === 1 ? '' : 's'}
        </span>
        {issue.hidden ? (
          <span className="badge" style={{ borderColor: 'var(--danger)', color: 'var(--danger)' }}>
            <Eye size={12} aria-hidden="true" /> Hidden
          </span>
        ) : null}
        {issue.locked ? (
          <span className="badge">
            <Lock size={12} aria-hidden="true" /> Locked
          </span>
        ) : null}
      </div>

      {repo.archived ? (
        <div className="alert alert-warning" role="status" style={{ marginBottom: 16 }}>
          <Archive size={16} aria-hidden="true" />
          <div>
            <b>This repository has been archived.</b> It is read-only: issues can be read and searched, but not opened, edited, commented on, closed or reopened. An admin can unarchive it from Settings.
          </div>
        </div>
      ) : null}
      {issue.hidden ? (
        <div className="alert alert-warning" role="status" style={{ marginBottom: 16 }}>
          <Eye size={16} aria-hidden="true" />
          <div style={{ flex: 1 }}>
            <b>This issue is hidden.</b>{' '}
            {hiddenEvent ? (
              <>
                Hidden by <b>{hiddenEvent.actor?.username ?? 'GitStack'}</b> {timeAgo(hiddenEvent.createdAt)}.{' '}
              </>
            ) : null}
            Only repository admins can see its content; the number #{issue.number} is kept and never reused.
            <div className="hidden-peek">
              <span className="small muted">What everyone else sees at this address:</span>
              <div className="row small" style={{ gap: 8, marginTop: 6 }}>
                <Eye size={14} className="muted" aria-hidden="true" />
                <span>
                  <b>#{issue.number}</b> · This issue has been hidden by a repository admin.
                </span>
              </div>
            </div>
          </div>
          {perm.canAdmin ? (
            <Button size="sm" onClick={() => void act(() => setHidden(ref, false))}>
              Unhide issue
            </Button>
          ) : null}
        </div>
      ) : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}

      <div className="issue-grid">
        <div className="timeline">
          <div className="cmt">
            <Avatar user={issue.author} />
            <div className="cmt-box">
              <div className="cmt-h">
                <b>{issue.author.username}</b>
                <AgentBadge user={issue.author} />
                <ViaToken via={issue.viaToken} />
                <span className="muted">
                  opened {timeAgo(issue.createdAt)}
                  {issue.edited ? <> · <EditedMark admin={perm.admin} load={() => fetchIssueVersions(ref)} label="issue" /></> : null}
                </span>
                <span className="sp" style={{ flex: 1 }} />
                <span className="badge">Author</span>
              </div>
              <div className="cmt-b">
                {editing ? (
                  <IssueEditor
                    owner={owner}
                    repo={name}
                    label="Issue description"
                    value={body}
                    onChange={setBody}
                    attachments={false}
                    actions={() => (
                      <>
                        <Button onClick={() => setEditing(false)}>Cancel</Button>
                        <Button variant="primary" disabled={!title.trim()} onClick={() => void saveEdit()}>
                          Save
                        </Button>
                      </>
                    )}
                  />
                ) : issue.body.trim() ? (
                  <Markdown source={issue.body} repo={{ owner, name }} />
                ) : (
                  <span className="muted">No description provided.</span>
                )}
                <Attachments list={issue.attachments} owner={owner} repo={name} />
              </div>
            </div>
          </div>
          {timeline}
          <CommentForm repo={repo} issue={issue} perm={perm} ref0={ref} act={act} />
        </div>
        <Sidebar data={data} perm={perm} ref0={ref} act={act} repoFullName={`${owner}/${name}`} />
      </div>
    </div>
  );
}

function Attachments({ list, owner, repo }: { list?: IssueAttachment[]; owner: string; repo: string }) {
  const [error, setError] = useState<string | null>(null);
  if (!list || list.length === 0) return null;
  return (
    <div className="stack" style={{ gap: 6, marginTop: 8 }}>
      {list.map((a) => (
        <button
          key={a.id}
          type="button"
          className="row small"
          style={{ gap: 6, padding: '3px 8px', border: '1px solid var(--border)', borderRadius: 'var(--r)', background: 'var(--surface-2)', display: 'inline-flex', cursor: 'pointer', alignSelf: 'flex-start' }}
          aria-label={`Download ${a.filename}`}
          onClick={() => downloadAttachment(owner, repo, a).catch((err: unknown) => setError(describeError(err)))}
        >
          <FileIcon size={14} aria-hidden="true" />
          <span className="mono">{a.filename}</span>
          <span className="muted">{formatBytes(a.size)}</span>
        </button>
      ))}
      {error ? <ErrorAlert message={error} /> : null}
    </div>
  );
}

/** «edited»: per admin apre le versioni precedenti (I4), per gli altri e' solo testo. */
function EditedMark({ admin, load, label }: { admin: boolean; load: () => Promise<TextVersion[]>; label: string }) {
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<TextVersion[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  if (!admin) return <span>edited</span>;
  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && versions === null) load().then(setVersions, (err: unknown) => setError(describeError(err)));
  };
  return (
    <>
      <button type="button" className="btn btn-ghost btn-sm" aria-expanded={open} title="Admins can view previous versions" onClick={toggle}>
        edited
      </button>
      {open ? (
        <div role="region" aria-label={`Previous versions of the ${label}`} className="card" style={{ position: 'absolute', zIndex: 5, padding: 12, maxWidth: 520 }}>
          {error ? <ErrorAlert message={error} /> : null}
          {versions?.length === 0 ? <span className="muted">No previous versions.</span> : null}
          {versions?.map((v) => (
            <div key={v.version} className="stack" style={{ gap: 4, marginBottom: 8 }}>
              <b className="small">
                Version {v.version} · {v.editor.username} · replaced {timeAgo(v.createdAt)}
              </b>
              {v.title ? <span className="small">{v.title}</span> : null}
              <pre className="cmdblock" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{v.body}</pre>
            </div>
          ))}
        </div>
      ) : null}
    </>
  );
}

type Perm = ReturnType<typeof permissions>;
type Ref = { owner: string; repo: string; number: number };

function CommentView({ c, issue, perm, me, ref0, act }: { c: IssueComment; issue: Issue; perm: Perm; me: string; ref0: Ref; act: Act }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(c.body);
  const [confirm, setConfirm] = useState(false);
  const mine = c.author.username === me;
  const canEdit = !perm.readOnly && mine && (!issue.locked || perm.write);
  const canDelete = !perm.readOnly && (mine || perm.admin);

  if (c.deleted) {
    return (
      <div className="ev" data-testid={`comment-${c.id}`}>
        <span className="evi">
          <UserIcon size={14} aria-hidden="true" />
        </span>
        <span>
          comment deleted
          {perm.admin ? <> · <EditedMark admin load={() => fetchCommentVersions(ref0, c.id)} label="comment" /></> : null}
        </span>
      </div>
    );
  }

  return (
    <div className="cmt" data-testid={`comment-${c.id}`}>
      <Avatar user={c.author} />
      <div className="cmt-box">
        <div className={mine ? 'cmt-h own' : 'cmt-h'}>
          <b>{c.author.username}</b>
          <AgentBadge user={c.author} />
          <ViaToken via={c.viaToken} />
          <span className="muted">
            commented {timeAgo(c.createdAt)}
            {c.edited ? <> · <EditedMark admin={perm.admin} load={() => fetchCommentVersions(ref0, c.id)} label="comment" /></> : null}
          </span>
          <span className="sp" style={{ flex: 1 }} />
          {c.author.username === issue.author.username ? <span className="badge">Author</span> : null}
          {canEdit && !editing ? (
            <Button variant="ghost" size="sm" aria-label={`Edit comment by ${c.author.username}`} onClick={() => { setText(c.body); setEditing(true); }}>
              Edit
            </Button>
          ) : null}
          {canDelete ? (
            <Button variant="ghost" size="sm" aria-label={`Delete comment by ${c.author.username}`} onClick={() => setConfirm(true)}>
              Delete
            </Button>
          ) : null}
        </div>
        <div className="cmt-b">
          {editing ? (
            <IssueEditor
              owner={ref0.owner}
              repo={ref0.repo}
              label="Edit comment"
              value={text}
              onChange={setText}
              attachments={false}
              actions={() => (
                <>
                  <Button onClick={() => setEditing(false)}>Cancel</Button>
                  <Button
                    variant="primary"
                    disabled={!text.trim()}
                    onClick={() => void act(() => editComment(ref0, c.id, text)).then((ok) => ok && setEditing(false))}
                  >
                    Update comment
                  </Button>
                </>
              )}
            />
          ) : (
            <Markdown source={c.body} repo={{ owner: ref0.owner, name: ref0.repo }} />
          )}
          <Attachments list={c.attachments} owner={ref0.owner} repo={ref0.repo} />
        </div>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Delete comment"
        description="The text is removed; the timeline keeps a «comment deleted» trace."
        confirmLabel="Delete comment"
        destructive
        onConfirm={() => void act(() => removeComment(ref0, c.id))}
      />
    </div>
  );
}

function nameOf(v: unknown): string {
  if (typeof v === 'string') return v;
  if (v && typeof v === 'object') {
    const o = v as Record<string, unknown>;
    for (const k of ['name', 'title', 'username']) if (typeof o[k] === 'string') return o[k] as string;
  }
  return '';
}

function EventView({ e }: { e: IssueEvent }) {
  const d = (e.data ?? {}) as Record<string, unknown>;
  const who = <b style={{ color: 'var(--text)' }}>{e.actor?.username ?? 'GitStack'}</b>;
  let icon: ReactNode = <TagIcon size={14} aria-hidden="true" />;
  let closed = false;
  let text: ReactNode;
  switch (e.type) {
    case 'closed': {
      const r = typeof d.reason === 'string' ? d.reason : 'completed';
      icon = <CircleCheck size={14} aria-hidden="true" />;
      closed = true;
      text = <>{who} closed this as {r === 'duplicate' && d.duplicateOf ? `duplicate of #${String(d.duplicateOf)}` : (REASON_TEXT[r] ?? r)}</>;
      break;
    }
    case 'reopened':
      icon = <CircleDot size={14} aria-hidden="true" />;
      text = <>{who} reopened this issue</>;
      break;
    case 'renamed':
      text = <>{who} changed the title from <i>{nameOf(d.from)}</i> to <i>{nameOf(d.to)}</i></>;
      break;
    case 'labeled':
    case 'unlabeled': {
      const l = d.label as { name?: string; color?: string } | string | undefined;
      const n = nameOf(l);
      const color = typeof l === 'object' && l?.color ? `#${l.color}` : undefined;
      text = <>{who} {e.type === 'labeled' ? 'added' : 'removed'} <span className="label" style={color ? ({ '--lc': color } as React.CSSProperties) : undefined}>{n}</span></>;
      break;
    }
    case 'assigned':
    case 'unassigned':
      icon = <UserIcon size={14} aria-hidden="true" />;
      text = <>{who} {e.type === 'assigned' ? 'assigned' : 'unassigned'} <b style={{ color: 'var(--text)' }}>{nameOf(d.assignee)}</b></>;
      break;
    case 'milestoned':
    case 'demilestoned':
      icon = <Flag size={14} aria-hidden="true" />;
      text = <>{who} {e.type === 'milestoned' ? 'added this to milestone' : 'removed this from milestone'} <b style={{ color: 'var(--text)' }}>{nameOf(d.milestone)}</b></>;
      break;
    case 'locked':
    case 'unlocked':
      icon = e.type === 'locked' ? <Lock size={14} aria-hidden="true" /> : <LockOpen size={14} aria-hidden="true" />;
      text = <>{who} {e.type === 'locked' ? 'locked the conversation' : 'unlocked the conversation'}{typeof d.reason === 'string' && d.reason ? ` (${d.reason})` : ''}</>;
      break;
    case 'hidden':
    case 'unhidden':
      icon = e.type === 'hidden' ? <EyeOff size={14} aria-hidden="true" /> : <Eye size={14} aria-hidden="true" />;
      text = <>{who} {e.type === 'hidden' ? 'hid this issue' : 'made this issue visible'}</>;
      break;
    default:
      text = <>{who} {e.type}</>;
  }
  return (
    <div className="ev" data-testid={`event-${e.type}`}>
      <span className={closed ? 'evi closed' : 'evi'}>{icon}</span>
      <span>
        {text} · {timeAgo(e.createdAt)}
      </span>
    </div>
  );
}

const CLOSE_OPTIONS: { value: IssueCloseReason; title: string; hint: string }[] = [
  { value: 'completed', title: 'Close as completed', hint: 'Done, fixed, shipped. Counts toward milestone progress.' },
  { value: 'not_planned', title: 'Close as not planned', hint: "Won't fix, can't reproduce, stale. Not counted in milestone progress." },
  { value: 'duplicate', title: 'Close as duplicate of…', hint: 'Not counted in milestone progress.' },
];

/** Pulsante diviso di chiusura con motivo (I2): il motivo scelto diventa l'etichetta del pulsante principale. */
function CloseControl({ issueNumber, onClose }: { issueNumber: number; onClose: (reason: IssueCloseReason, duplicateOf?: number) => void }) {
  const [reason, setReason] = useState<IssueCloseReason>('completed');
  const [dup, setDup] = useState('');
  const [open, setOpen] = useState(false);
  const dupN = Number(dup);
  const dupOk = Number.isInteger(dupN) && dupN > 0 && dupN !== issueNumber;
  const disabled = reason === 'duplicate' && !dupOk;
  const label = reason === 'duplicate' ? (dupOk ? `Close as duplicate of #${dupN}` : 'Close as duplicate of…') : CLOSE_OPTIONS.find((o) => o.value === reason)?.title;
  const icon = (r: IssueCloseReason) => (r === 'completed' ? <Check size={16} aria-hidden="true" style={{ color: 'var(--closed)' }} /> : r === 'not_planned' ? <X size={16} aria-hidden="true" className="muted" /> : <Copy size={16} aria-hidden="true" className="muted" />);

  return (
    <span style={{ position: 'relative', display: 'inline-flex' }}>
      <span className="splitbtn">
        <Button disabled={disabled} onClick={() => onClose(reason, reason === 'duplicate' ? dupN : undefined)}>
          <CircleCheck size={16} aria-hidden="true" style={{ color: 'var(--closed)' }} />
          {label}
        </Button>
        <Button aria-label="Other close reasons" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <ChevronDown size={16} aria-hidden="true" />
        </Button>
      </span>
      {open ? (
        <div className="menu close-menu" role="menu" aria-label="Close reasons" style={{ right: 0, bottom: 'calc(100% + 6px)' }}>
          {CLOSE_OPTIONS.map((o) => (
            <div key={o.value} className={reason === o.value ? 'mi on' : 'mi'}>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={reason === o.value}
                className="mi-btn"
                onClick={() => {
                  setReason(o.value);
                  if (o.value !== 'duplicate') setOpen(false);
                }}
              >
                {icon(o.value)}
                <span>
                  <b>{o.title}</b>
                  <span className="small muted" style={{ display: 'block', fontWeight: 400 }}>{o.hint}</span>
                </span>
              </button>
              {reason === o.value ? <Check size={16} aria-hidden="true" style={{ marginLeft: 'auto', color: 'var(--accent)' }} /> : null}
              {o.value === 'duplicate' && reason === 'duplicate' ? (
                <input
                  className="input mono"
                  style={{ height: 28, width: 100, fontSize: 12 }}
                  aria-label="Duplicate of issue number"
                  placeholder="#n"
                  inputMode="numeric"
                  value={dup}
                  onChange={(e) => setDup(e.target.value.replace(/[^0-9]/g, ''))}
                />
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
    </span>
  );
}

function CommentForm({ repo, issue, perm, ref0, act }: { repo: Repository; issue: Issue; perm: Perm; ref0: Ref; act: Act }) {
  const [text, setText] = useState('');
  const owner = repo.owner.name;

  let notice: ReactNode = null;
  if (repo.archived) {
    notice = (
      <div className="alert alert-warning" role="status" style={{ marginLeft: 52 }}>
        <Archive size={16} aria-hidden="true" />
        <div>The repository is archived: commenting, closing and reopening are disabled.</div>
      </div>
    );
  } else if (!perm.canComment && issue.locked) {
    notice = (
      <div className="alert alert-info" role="status" style={{ marginLeft: 52 }}>
        <Lock size={16} aria-hidden="true" />
        <div>
          <b>This conversation has been locked.</b> Only people with Write access to this repository can comment. You can still read the issue, follow it and open a new one.
        </div>
      </div>
    );
  } else if (!perm.canComment) {
    notice = <p className="muted small" role="status">You cannot comment on this issue.</p>;
  }

  if (!perm.canComment && !perm.canCloseReopen) return <>{notice}</>;

  return (
    <div className="cmt">
      <span className="avatar avatar-lg" aria-hidden="true">+</span>
      <div style={{ flex: 1 }}>
        {notice ? <div style={{ marginLeft: -52, marginBottom: 8 }}>{notice}</div> : null}
        {perm.canComment ? (
          <IssueEditor
            owner={owner}
            repo={repo.name}
            label="Leave a comment"
            placeholder="Leave a comment. Markdown, @mentions and #references supported."
            value={text}
            onChange={setText}
            actions={({ attachmentIds, clear }) => (
              <>
                {perm.canCloseReopen && issue.state === 'open' ? (
                  <CloseControl
                    issueNumber={issue.number}
                    onClose={(reason, dupN) =>
                      void act(async () => {
                        if (text.trim()) await postComment(ref0, text, attachmentIds);
                        await closeIssueWith(ref0, reason, dupN);
                      }).then((ok) => { if (ok) { setText(''); clear(); } })
                    }
                  />
                ) : null}
                {perm.canCloseReopen && issue.state === 'closed' ? (
                  <Button onClick={() => void act(() => reopen(ref0))}>Reopen issue</Button>
                ) : null}
                <Button variant="primary" disabled={!text.trim()} onClick={() => void act(() => postComment(ref0, text, attachmentIds)).then((ok) => { if (ok) { setText(''); clear(); } })}>
                  Comment
                </Button>
              </>
            )}
          />
        ) : (
          <div className="row">
            {perm.canCloseReopen && issue.state === 'closed' ? <Button onClick={() => void act(() => reopen(ref0))}>Reopen issue</Button> : null}
            {perm.canCloseReopen && issue.state === 'open' ? (
              <CloseControl issueNumber={issue.number} onClose={(reason, dupN) => void act(() => closeIssueWith(ref0, reason, dupN))} />
            ) : null}
          </div>
        )}
      </div>
    </div>
  );
}

function Sidebar({ data, perm, ref0, act, repoFullName }: { data: Data; perm: Perm; ref0: Ref; act: Act; repoFullName: string }) {
  const { issue, labels, milestones } = data;
  const [edit, setEdit] = useState<'assignees' | 'labels' | 'milestone' | null>(null);
  const [assignee, setAssignee] = useState('');
  const [hideAsk, setHideAsk] = useState(false);
  const toggle = (k: 'assignees' | 'labels' | 'milestone') => setEdit((cur) => (cur === k ? null : k));
  const gear = (k: 'assignees' | 'labels' | 'milestone', label: string) =>
    perm.canSidebar ? (
      <button type="button" className="btn btn-ghost btn-sm" style={{ marginLeft: 'auto' }} aria-label={`Edit ${label}`} aria-expanded={edit === k} onClick={() => toggle(k)}>
        <Settings size={14} aria-hidden="true" />
      </button>
    ) : null;

  const names = issue.assignees.map((a) => a.username);
  const ms = issue.milestone ? milestones.find((m) => m.number === issue.milestone?.number) : undefined;
  const total = ms ? ms.openIssues + ms.closedIssues : 0;
  const pct = ms && total > 0 ? Math.round((ms.closedIssues / total) * 100) : 0;
  const labelNames = issue.labels.map((l) => l.name);

  return (
    <aside aria-label="Issue sidebar">
      <div className="side-sec">
        <h4>Assignees{gear('assignees', 'assignees')}</h4>
        {issue.assignees.length === 0 ? <span className="small muted">No one</span> : null}
        {issue.assignees.map((a) => (
          <div key={a.id} className="row" style={{ marginBottom: 4 }}>
            <span className={a.kind === 'agent' ? 'avatar av-bot' : 'avatar'} aria-hidden="true">
              {a.kind === 'agent' ? <Bot size={14} /> : a.username.slice(0, 2).toUpperCase()}
            </span>
            <b>{a.username}</b>
            <AgentBadge user={a} />
            {edit === 'assignees' ? (
              <Button variant="ghost" size="sm" aria-label={`Unassign ${a.username}`} onClick={() => void act(() => putAssignees(ref0, names.filter((n) => n !== a.username)))}>
                Remove
              </Button>
            ) : null}
          </div>
        ))}
        {edit === 'assignees' ? (
          <form
            className="row"
            style={{ flexWrap: 'nowrap', marginTop: 6 }}
            onSubmit={(e) => {
              e.preventDefault();
              const u = assignee.trim();
              if (!u || names.includes(u)) return;
              void act(() => putAssignees(ref0, [...names, u])).then((ok) => ok && setAssignee(''));
            }}
          >
            <input className="input" aria-label="Assignee username" placeholder="username (needs Write)" value={assignee} onChange={(e) => setAssignee(e.target.value)} />
            <Button type="submit" size="sm" disabled={!assignee.trim()}>
              Assign
            </Button>
          </form>
        ) : null}
        {edit === 'assignees' && data.me && !names.includes(data.me) && perm.write ? (
          <Button variant="ghost" size="sm" onClick={() => void act(() => putAssignees(ref0, [...names, data.me]))}>
            Assign yourself
          </Button>
        ) : null}
      </div>

      <div className="side-sec">
        <h4>Labels{gear('labels', 'labels')}</h4>
        {issue.labels.length === 0 ? <span className="small muted">None yet</span> : null}
        <div className="row" style={{ gap: 6 }}>
          {issue.labels.map((l) => (
            <span key={l.id} className="label" style={{ '--lc': `#${l.color}` } as React.CSSProperties}>
              {l.name}
            </span>
          ))}
        </div>
        {edit === 'labels' ? (
          <div className="stack" style={{ gap: 4, marginTop: 8 }} role="group" aria-label="Labels of this issue">
            {labels.length === 0 ? <span className="small muted">The repository has no labels.</span> : null}
            {labels.map((l) => (
              <label key={l.id} className="check">
                <input
                  type="checkbox"
                  checked={labelNames.includes(l.name)}
                  onChange={(e) => void act(() => putLabels(ref0, e.target.checked ? [...labelNames, l.name] : labelNames.filter((n) => n !== l.name)))}
                />
                <span>{l.name}</span>
              </label>
            ))}
          </div>
        ) : null}
      </div>

      <div className="side-sec">
        <h4>Milestone{gear('milestone', 'milestone')}</h4>
        {issue.milestone ? (
          <>
            <div className="row" style={{ marginBottom: 6 }}>
              <Flag size={14} aria-hidden="true" />
              <b>{issue.milestone.title}</b>
              {ms?.dueOn ? <span className="small muted" style={{ marginLeft: 'auto' }}>due {ms.dueOn}</span> : null}
            </div>
            {ms ? (
              <>
                <div className="progress" role="progressbar" aria-label="Milestone progress" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
                  <i style={{ width: `${pct}%` }} />
                </div>
                <div className="small muted" style={{ marginTop: 4 }}>
                  {pct}% complete · {ms.closedIssues} of {total} closed
                </div>
              </>
            ) : null}
          </>
        ) : (
          <span className="small muted">No milestone</span>
        )}
        {edit === 'milestone' ? (
          <select
            className="select"
            style={{ marginTop: 8 }}
            aria-label="Milestone"
            value={issue.milestone?.number ?? ''}
            onChange={(e) => void act(() => putMilestone(ref0, e.target.value ? Number(e.target.value) : null))}
          >
            <option value="">No milestone</option>
            {milestones.filter((m) => m.state === 'open' || m.number === issue.milestone?.number).map((m) => (
              <option key={m.number} value={m.number}>
                {m.title}
              </option>
            ))}
          </select>
        ) : null}
      </div>

      <LinkedCommitsBox commits={linkedCommits(data.events)} repoFullName={repoFullName} />

      {perm.admin ? (
        <div className="side-sec">
          <h4>Admin</h4>
          {perm.canAdmin ? (
            <div className="stack" style={{ gap: 6 }}>
              <Button size="sm" style={{ width: '100%', justifyContent: 'center' }} onClick={() => void act(() => setLocked(ref0, !issue.locked))}>
                {issue.locked ? <LockOpen size={14} aria-hidden="true" /> : <Lock size={14} aria-hidden="true" />}
                {issue.locked ? 'Unlock conversation' : 'Lock conversation'}
              </Button>
              <Button variant={issue.hidden ? 'default' : 'danger'} size="sm" style={{ width: '100%', justifyContent: 'center' }} onClick={() => (issue.hidden ? void act(() => setHidden(ref0, false)) : setHideAsk(true))}>
                <Eye size={14} aria-hidden="true" />
                {issue.hidden ? 'Unhide issue' : 'Hide issue'}
              </Button>
            </div>
          ) : (
            <p className="small muted">The repository is archived: read-only.</p>
          )}
          <p className="small muted" style={{ marginTop: 6 }}>
            Locked: only people with Write can comment. Hidden: content visible to admins only; the number is kept.
          </p>
        </div>
      ) : null}

      <IssueSubscriptionBox owner={ref0.owner} repo={ref0.repo} number={ref0.number} />

      {data.role === 'read' && !perm.write ? (
        <div className="side-sec" style={{ border: 0 }}>
          <p className="small muted" style={{ margin: 0 }}>
            You have <b>Read</b> access: you can open issues and comment. Assignees, labels, milestone and closing issues of others need <b>Write</b>.
          </p>
        </div>
      ) : null}
      <ConfirmDialog
        open={hideAsk}
        onOpenChange={setHideAsk}
        title="Hide issue"
        description="The content becomes visible to admins only; everyone else gets «not found». The number is kept."
        confirmLabel="Hide issue"
        destructive
        onConfirm={() => void act(() => setHidden(ref0, true))}
      />
    </aside>
  );
}
