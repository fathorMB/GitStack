import { AlertTriangle, Check, ChevronDown, GitBranch, Pencil, Eye, X } from 'lucide-react';
import { useState } from 'react';
import type { CombinedCommitStatus, MergeMethod, PullRequest, PullRequestMergeability, PullRequestReview } from '../../lib/pullsApi';

const METHOD_LABEL: Record<MergeMethod, string> = { squash: 'Squash and merge', merge: 'Create a merge commit' };

export interface PullMergeBoxProps {
  pr: PullRequest;
  /** null finche' non e' arrivato (o se il calcolo e' fallito). */
  mergeability: PullRequestMergeability | null;
  status: CombinedCommitStatus | null;
  reviews: PullRequestReview[];
  /** Ruolo `write` (o admin) sul repo, non archiviato: unisce, ripristina, elimina il branch. */
  canWrite: boolean;
  /** Chi ha aperto la PR puo' segnarla pronta. */
  isAuthor: boolean;
  busy?: boolean;
  onMerge: (method: MergeMethod, deleteBranch: boolean) => void;
  onReady: () => void;
  onDeleteBranch: () => void;
  onRerequest: (usernames: string[]) => void;
}

type Tone = 'ok' | 'warn' | 'bad' | '';

function Row({ tone, icon, children, action }: { tone: Tone; icon: React.ReactNode; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-row">
      <span className={`mb-i ${tone}`.trim()} aria-hidden="true">
        {icon}
      </span>
      <div style={{ flex: 1 }}>{children}</div>
      {action}
    </div>
  );
}

const tick = <Check size={14} />;

/**
 * Riquadro di merge della Conversation (mockup 26). La variante si ricava dai
 * dati, non da un interruttore: bozza, unita, chiusa, oppure aperta con le tre
 * righe approvazioni / stati / conflitti (PR2, PR4, PR6, PR7, PR9).
 */
export function PullMergeBox(props: PullMergeBoxProps) {
  const { pr, canWrite, isAuthor, busy } = props;

  if (pr.state === 'merged') {
    return (
      <div className="mergebox merged" data-variant="merged">
        <Row
          tone=""
          icon={<GitBranch size={14} />}
          action={
            canWrite && pr.sourceBranchExists ? (
              <button type="button" className="btn" disabled={busy} onClick={props.onDeleteBranch}>
                Delete branch
              </button>
            ) : null
          }
        >
          <b>Pull request successfully merged and closed</b>
          <div className="small muted">
            {pr.sourceBranchExists ? (
              <>
                The <span className="mono">{pr.sourceBranch}</span> branch can be safely deleted.{' '}
              </>
            ) : (
              <>
                The <span className="mono">{pr.sourceBranch}</span> branch has been deleted.{' '}
              </>
            )}
            A merged pull request can&apos;t be reopened.
          </div>
        </Row>
      </div>
    );
  }

  if (pr.state === 'closed') {
    return (
      <div className="mergebox" data-variant="closed">
        <Row tone="" icon={<X size={14} />}>
          <b>This pull request is closed</b>
          <div className="small muted">Reopen it to merge. Nothing has been merged into <span className="mono">{pr.targetBranch}</span>.</div>
        </Row>
      </div>
    );
  }

  if (pr.draft) {
    return (
      <div className="mergebox" data-variant="draft">
        <Row
          tone=""
          icon={<Pencil size={14} />}
          action={
            canWrite || isAuthor ? (
              <button type="button" className="btn" disabled={busy} onClick={props.onReady}>
                Ready for review
              </button>
            ) : null
          }
        >
          <b>This pull request is still a draft</b>
          <div className="small muted">
            It can&apos;t be merged and no review has been requested yet. When the work is done, mark it ready: the requested reviewers will be notified.
          </div>
        </Row>
      </div>
    );
  }

  return <OpenBox {...props} />;
}

function OpenBox({ pr, mergeability: m, status, reviews, canWrite, busy, onMerge, onRerequest }: PullMergeBoxProps) {
  const [method, setMethod] = useState<MergeMethod | null>(null);
  const [menu, setMenu] = useState(false);
  const [deleteBranch, setDeleteBranch] = useState(false);

  const blockers = m?.blockers ?? [];
  const has = (...codes: string[]) => blockers.some((b) => codes.includes(b.code));
  const conflicts = m?.state === 'conflicts' || has('conflicts');
  const approvalBlocked = has('approvals_required', 'changes_requested');
  const statusBlocked = has('status_required', 'status_failed');
  const failing = statusBlocked && (has('status_failed') || status?.state === 'failure' || status?.state === 'error');

  const allowed = m?.allowedMethods ?? [];
  const chosen: MergeMethod = method && allowed.includes(method) ? method : (m?.defaultMethod ?? allowed[0] ?? 'squash');
  const other = allowed.find((a) => a !== chosen);
  const canMerge = canWrite && m?.mergeable === true;
  const need = m?.requiredApprovals ?? pr.requiredApprovals;
  const have = m?.approvals ?? pr.approvals;

  // Approvazioni: persone che contano; agenti e approvazioni superate dai nuovi commit solo mostrate (PR2, PR4).
  const agentApprovers = reviews.filter((r) => r.state === 'approved' && r.author.kind === 'agent' && !r.stale).map((r) => r.author.username);
  const dismissed = reviews.filter((r) => r.state === 'approved' && r.stale && r.author.kind === 'human').map((r) => r.author.username);
  const approvers = reviews.filter((r) => r.state === 'approved' && r.countsAsApproval).map((r) => r.author.username);
  const changesBy = reviews.filter((r) => r.state === 'changes_requested' && !r.stale).map((r) => r.author.username);

  let approvalTone: Tone = 'ok';
  if (has('changes_requested') || pr.reviewDecision === 'changes_requested') approvalTone = 'bad';
  else if (approvalBlocked || pr.reviewDecision === 'required') approvalTone = 'warn';

  const statuses = status?.statuses ?? [];
  const requiredCtx = new Set((status?.requiredContexts ?? []).map((c) => c.context));
  const missing = (status?.requiredContexts ?? []).filter((c) => !c.satisfied && !statuses.some((s) => s.context === c.context));
  const pending = status?.state === 'pending';
  const statusTone: Tone = failing ? 'bad' : statusBlocked || pending ? 'warn' : 'ok';
  const statusTitle = failing ? 'Some required checks failed' : statusBlocked ? 'Required checks have not run yet' : pending ? 'Checks are running' : statuses.length === 0 ? 'No checks reported' : 'All required checks passed';

  let footNote: React.ReactNode = null;
  if (!canWrite) footNote = 'Merging a pull request needs Write access to this repository.';
  else if (conflicts) footNote = 'The browser does not edit conflicts.';
  else if (failing || statusBlocked) {
    const ctx = blockers.find((b) => b.code === 'status_required' || b.code === 'status_failed')?.contexts?.[0];
    footNote = (
      <>
        Required check {ctx ? <span className="mono">{ctx}</span> : 'results'} must succeed on the latest commit.
      </>
    );
  } else if (approvalBlocked)
    footNote = (
      <>
        Merging is blocked by the rules of <span className="mono">{pr.targetBranch}</span>. They apply to everyone, admins included.
      </>
    );
  else if (m && !m.mergeable) footNote = blockers[0]?.message ?? 'This pull request can’t be merged yet.';
  else if (!m) footNote = 'Checking whether this pull request can be merged…';

  const tone: Tone = conflicts || failing || approvalTone === 'bad' ? 'bad' : approvalBlocked || statusBlocked ? 'warn' : 'ok';
  const variant = conflicts ? 'conflicts' : failing ? 'failing' : approvalBlocked ? 'approval' : statusBlocked ? 'status-required' : 'ready';

  return (
    <div className={`mergebox ${tone}`} data-variant={variant}>
      <Row
        tone={approvalTone}
        icon={approvalTone === 'ok' ? tick : approvalTone === 'bad' ? <X size={14} /> : <Eye size={14} />}
        action={
          approvalTone === 'warn' && canWrite && dismissed.length > 0 ? (
            <button type="button" className="btn btn-sm" disabled={busy} onClick={() => onRerequest(dismissed)}>
              Re-request review
            </button>
          ) : null
        }
      >
        <b>{approvalTone === 'ok' ? 'Approved' : approvalTone === 'bad' ? 'Changes requested' : 'Review required'}</b>
        <div className="small muted">
          {approvalTone === 'ok' ? (
            <>
              {have} of {need} required {need === 1 ? 'approval' : 'approvals'} from a person{approvers.length ? ` (${approvers.join(', ')})` : ''}. Agent approvals are shown but don&apos;t count.
            </>
          ) : approvalTone === 'bad' ? (
            <>{changesBy.length ? `${changesBy.join(', ')} requested changes.` : 'A reviewer requested changes.'}</>
          ) : (
            <>
              At least {need || 1} {need === 1 || need === 0 ? 'approval' : 'approvals'} from a person with Write is required.
              {agentApprovers.length ? (
                <>
                  {' '}
                  <b>{agentApprovers.join(', ')}</b> <span className="badge badge-agent">agent</span> approved, but agent approvals don&apos;t count.
                </>
              ) : null}
              {dismissed.length ? ` ${dismissed.join(', ')}’s approval was dismissed by new commits.` : ''}
            </>
          )}
        </div>
      </Row>

      <Row tone={statusTone} icon={statusTone === 'ok' ? tick : statusTone === 'bad' ? <X size={14} /> : <Eye size={14} />}>
        <b>{statusTitle}</b>
        {statuses.length || missing.length ? (
          <div className="checks">
            {statuses.map((s) => (
              <div key={s.id}>
                <span className={s.state === 'success' ? 'pr-ok' : s.state === 'pending' ? 'muted' : 'pr-ko'} aria-label={s.state}>
                  {s.state === 'success' ? '✓' : s.state === 'pending' ? '◌' : '✗'}
                </span>{' '}
                <b>{s.context}</b>
                {requiredCtx.has(s.context) ? <span className="badge">Required</span> : null}
                {s.description ? <span className="muted">{s.description}</span> : null}
                {s.targetUrl ? (
                  <a className="small" href={s.targetUrl} target="_blank" rel="noopener noreferrer">
                    Details
                  </a>
                ) : null}
              </div>
            ))}
            {missing.map((c) => (
              <div key={c.context}>
                <span className="muted">◌</span> <b>{c.context}</b>
                <span className="badge">Required</span>
                <span className="muted">Expected, not reported yet</span>
              </div>
            ))}
          </div>
        ) : null}
      </Row>

      <Row tone={conflicts ? 'bad' : m ? 'ok' : ''} icon={conflicts ? <AlertTriangle size={14} /> : tick}>
        {conflicts ? (
          <>
            <b>This branch has conflicts that must be resolved</b>
            {m && m.conflictFiles.length > 0 ? (
              <>
                <div className="small muted" style={{ margin: '4px 0 6px' }}>
                  Conflicting files:
                </div>
                <ul className="mono small" style={{ margin: 0, padding: 0, listStyle: 'none' }} aria-label="Conflicting files">
                  {m.conflictFiles.map((f) => (
                    <li key={f}>{f}</li>
                  ))}
                  {m.conflictFilesTruncated ? <li className="muted">…and more</li> : null}
                </ul>
              </>
            ) : null}
            <div className="small muted" style={{ margin: '10px 0 4px' }}>
              Resolve them on your machine, then push to <span className="mono">{pr.sourceBranch}</span>:
            </div>
            <pre className="cmdblock" style={{ margin: 0 }}>{`gs pr checkout ${pr.number}\ngit merge origin/${pr.targetBranch}\n# fix the conflicts, then\ngit commit && git push`}</pre>
            <div className="small muted" style={{ marginTop: 6 }}>
              Agents read the same list with <span className="mono">gs pr view {pr.number} --json mergeable,conflicts</span>.
            </div>
          </>
        ) : (
          <>
            <b>{m ? 'No conflicts with the base branch' : 'Checking for conflicts…'}</b>
            {m && m.state === 'mergeable' ? <div className="small muted">Merging can be performed automatically.</div> : null}
          </>
        )}
      </Row>

      <div className="mb-foot">
        {canWrite ? (
          <>
            <span className="splitbtn" style={{ position: 'relative' }}>
              <button type="button" className="btn btn-primary" disabled={!canMerge || busy} onClick={() => onMerge(chosen, deleteBranch)}>
                {METHOD_LABEL[chosen]}
              </button>
              <button
                type="button"
                className="btn btn-primary"
                aria-label="Merge method"
                aria-haspopup="menu"
                aria-expanded={menu}
                disabled={allowed.length < 2}
                onClick={() => setMenu((v) => !v)}
              >
                <ChevronDown size={14} aria-hidden="true" />
              </button>
              {menu ? (
                <div className="mb-menu" role="menu" aria-label="Merge method" style={{ top: '100%', left: 0 }}>
                  {allowed.map((a) => (
                    <button
                      key={a}
                      type="button"
                      role="menuitemradio"
                      aria-checked={a === chosen}
                      onClick={() => {
                        setMethod(a);
                        setMenu(false);
                      }}
                    >
                      {METHOD_LABEL[a]}
                    </button>
                  ))}
                </div>
              ) : null}
            </span>
            {canMerge && other ? (
              <span className="small muted">
                or{' '}
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setMethod(other)}>
                  {METHOD_LABEL[other]}
                </button>
              </span>
            ) : null}
          </>
        ) : null}
        {footNote ? <span className="small muted">{footNote}</span> : null}
        <span className="sp" />
        {canWrite ? (
          <label className="check small" style={{ margin: 0 }}>
            <input type="checkbox" checked={deleteBranch} onChange={(e) => setDeleteBranch(e.target.checked)} />
            <span>Delete branch after merge</span>
          </label>
        ) : null}
      </div>
    </div>
  );
}
