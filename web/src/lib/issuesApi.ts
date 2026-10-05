// Issues (M-05) via client generato (client di default: l'interceptor 401 le vede).
import {
  closeIssue,
  createIssue,
  createIssueComment,
  deleteIssueComment,
  getIssue,
  getIssueAttachment,
  getMyResourcePermission,
  listIssueCommentVersions,
  listIssueComments,
  listIssueEvents,
  listIssues,
  listIssueTemplates,
  listUsers,
  listIssueVersions,
  listLabels,
  listMilestones,
  lockIssue,
  reopenIssue,
  setIssueAssignees,
  setIssueHidden,
  setIssueLabels,
  setIssueMilestone,
  unlockIssue,
  updateIssue,
  updateIssueComment,
  uploadIssueAttachment,
} from '@gitstack/api-client';
import type { CreateIssueInput, IssueTemplate, User, Issue, IssueAttachment, IssueCloseReason, IssueComment, IssueCommentList, IssueEvent, IssueEventList, TextVersion, IssueList, IssueSummary, IssueUser, Label, LabelList, Milestone, MilestoneList } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { CreateIssueInput, IssueTemplate, User, Issue, IssueAttachment, IssueCloseReason, IssueComment, IssueEvent, TextVersion, IssueList, IssueSummary, IssueUser, Label, Milestone };

export type IssueSort = 'created' | 'updated' | 'comments' | 'relevance';

/**
 * Lo stato sta in `q` (is:open/is:closed): `state=all` evita che il default
 * `open` dell'API si sommi a un `is:closed` della barra.
 */
export async function fetchIssues(owner: string, repo: string, q: string, sort: IssueSort | '', page = 1, perPage = 30): Promise<IssueList> {
  return unwrap(
    await listIssues({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      query: { ...(q.trim() ? { q: q.trim() } : {}), state: 'all', ...(sort ? { sort } : {}), page, perPage },
    }),
  );
}

/** Solo il totale delle issues che rispondono a `q`. */
export async function countIssues(owner: string, repo: string, q: string): Promise<number> {
  return (await fetchIssues(owner, repo, q, '', 1, 1)).total;
}

export async function fetchLabels(owner: string, repo: string): Promise<LabelList> {
  return unwrap(await listLabels({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { perPage: 100 } }));
}

export async function fetchMilestones(owner: string, repo: string): Promise<MilestoneList> {
  return unwrap(await listMilestones({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { state: 'all' } }));
}

// ---- nuova issue (M-05/K) ----

/** Modelli di .gitstack/ISSUE_TEMPLATE/ (I11). */
export async function fetchTemplates(owner: string, repo: string): Promise<IssueTemplate[]> {
  return unwrap(await listIssueTemplates({ baseUrl: API_BASE_URL, path: { owner, repo } })).items;
}

export async function createNewIssue(owner: string, repo: string, input: CreateIssueInput): Promise<Issue> {
  return unwrap(await createIssue({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

/** Persone e agenti per prefisso (suggerimenti di @menzione, I8). */
export async function searchUsers(q: string, perPage = 8): Promise<User[]> {
  return unwrap(await listUsers({ baseUrl: API_BASE_URL, query: { q, perPage } })).items;
}

// ---- dettaglio issue (M-05/J) ----

export type Role = 'read' | 'write' | 'admin' | null;

/** Ruolo effettivo del chiamante sul repo (getMyResourcePermission): non si indovina. */
export async function fetchMyRole(resourceId: string): Promise<Role> {
  const p = unwrap(await getMyResourcePermission({ baseUrl: API_BASE_URL, path: { resourceId } }));
  return (p.role ?? null) as Role;
}

type Ref = { owner: string; repo: string; number: number };
const at = ({ owner, repo, number }: Ref) => ({ baseUrl: API_BASE_URL, path: { owner, repo, number } });

export async function fetchIssue(r: Ref): Promise<Issue> {
  return unwrap(await getIssue(at(r)));
}

export async function fetchIssueEvents(r: Ref): Promise<IssueEventList> {
  return unwrap(await listIssueEvents({ ...at(r), query: { perPage: 100 } }));
}

export async function fetchIssueComments(r: Ref): Promise<IssueCommentList> {
  return unwrap(await listIssueComments({ ...at(r), query: { perPage: 100 } }));
}

export async function fetchIssueVersions(r: Ref): Promise<TextVersion[]> {
  return unwrap(await listIssueVersions(at(r))).items;
}

export async function fetchCommentVersions(r: Ref, commentId: string): Promise<TextVersion[]> {
  return unwrap(await listIssueCommentVersions({ baseUrl: API_BASE_URL, path: { ...at(r).path, commentId } })).items;
}

export async function editIssue(r: Ref, input: { title?: string; body?: string }): Promise<Issue> {
  return unwrap(await updateIssue({ ...at(r), body: input }));
}

export async function closeIssueWith(r: Ref, reason: IssueCloseReason, duplicateOf?: number): Promise<Issue> {
  return unwrap(await closeIssue({ ...at(r), body: { reason, ...(reason === 'duplicate' && duplicateOf ? { duplicateOf } : {}) } }));
}

export async function reopen(r: Ref): Promise<Issue> {
  return unwrap(await reopenIssue(at(r)));
}

export async function setHidden(r: Ref, hidden: boolean): Promise<Issue> {
  return unwrap(await setIssueHidden({ ...at(r), body: { hidden } }));
}

export async function setLocked(r: Ref, locked: boolean): Promise<Issue> {
  return unwrap(locked ? await lockIssue({ ...at(r), body: {} }) : await unlockIssue(at(r)));
}

export async function putAssignees(r: Ref, assignees: string[]): Promise<Issue> {
  return unwrap(await setIssueAssignees({ ...at(r), body: { assignees } }));
}

export async function putLabels(r: Ref, labels: string[]): Promise<Issue> {
  return unwrap(await setIssueLabels({ ...at(r), body: { labels } }));
}

export async function putMilestone(r: Ref, milestone: number | null): Promise<Issue> {
  return unwrap(await setIssueMilestone({ ...at(r), body: { milestone } }));
}

export async function postComment(r: Ref, body: string, attachmentIds: string[]): Promise<IssueComment> {
  return unwrap(await createIssueComment({ ...at(r), body: { body, ...(attachmentIds.length ? { attachmentIds } : {}) } }));
}

export async function editComment(r: Ref, commentId: string, body: string): Promise<IssueComment> {
  return unwrap(await updateIssueComment({ baseUrl: API_BASE_URL, path: { ...at(r).path, commentId }, body: { body } }));
}

export async function removeComment(r: Ref, commentId: string): Promise<void> {
  const res = await deleteIssueComment({ baseUrl: API_BASE_URL, path: { ...at(r).path, commentId } });
  if (res.error) unwrap({ error: res.error, response: res.response } as never);
}

/** Limite lato client degli allegati (I9): oltre, il server risponde 413. */
export const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024;

export async function uploadAttachment(owner: string, repo: string, file: File): Promise<IssueAttachment> {
  return unwrap(await uploadIssueAttachment({ baseUrl: API_BASE_URL, path: { owner, repo }, body: { file } }));
}

/** Scarica un allegato autenticato e lo salva (nessun URL pubblico, I9). */
export async function downloadAttachment(owner: string, repo: string, a: IssueAttachment): Promise<void> {
  const res = await getIssueAttachment({ baseUrl: API_BASE_URL, path: { owner, repo, attachmentId: a.id }, parseAs: 'blob' });
  const blob = unwrap(res as { data?: Blob; error?: unknown; response?: Response });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = a.filename;
  link.click();
  URL.revokeObjectURL(url);
}
