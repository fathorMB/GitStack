// Pull request (v1.1) via client generato (client di default: l'interceptor 401 le vede).
import {
  closePullRequest,
  createPullRequest,
  createPullRequestComment,
  deletePullRequestBranch,
  getCombinedCommitStatus,
  listCombinedCommitStatuses,
  getPullRequest,
  getPullRequestMergeability,
  createPullRequestReview,
  listPullRequestCommits,
  listPullRequestFiles,
  listPullRequestThreads,
  resolvePullRequestThread,
  setPullRequestFileViewed,
  unresolvePullRequestThread,
  listPullRequestComments,
  listPullRequestReviews,
  listPullRequests,
  markPullRequestDraft,
  markPullRequestReady,
  mergePullRequest,
  removePullRequestReviewer,
  reopenPullRequest,
  requestPullRequestReviewers,
} from '@gitstack/api-client';
import type {
  CombinedCommitStatus,
  CreatePullRequestCommentInput,
  CreatePullRequestInput,
  DiffSide,
  MergeMethod,
  PullRequest,
  PullRequestComment,
  PullRequestFiles,
  PullRequestList,
  PullRequestMergeability,
  PullRequestReview,
  PullRequestSummary,
  PullRequestThread,
  ReviewEvent,
  CommitSummary,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CombinedCommitStatus, CreatePullRequestInput, MergeMethod, PullRequest, DiffSide, PullRequestComment, PullRequestFiles, PullRequestList, PullRequestMergeability, PullRequestReview, PullRequestSummary, PullRequestThread, ReviewEvent };

export interface PullRef {
  owner: string;
  repo: string;
  number: number;
}
const at = ({ owner, repo, number }: PullRef) => ({ baseUrl: API_BASE_URL, path: { owner, repo, number } });

/** Stessa sintassi di ricerca delle issues (PR10): lo stato sta in `q`, `state=all` evita il default `open`. */
export async function fetchPulls(owner: string, repo: string, q: string, sort: 'created' | 'updated' | 'comments' | '', page = 1, perPage = 30): Promise<PullRequestList> {
  return unwrap(
    await listPullRequests({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      query: { ...(q.trim() ? { q: q.trim() } : {}), state: 'all', ...(sort ? { sort } : {}), page, perPage },
    }),
  );
}

export async function countPulls(owner: string, repo: string, q: string): Promise<number> {
  return (await fetchPulls(owner, repo, q, '', 1, 1)).total;
}

export async function createPull(owner: string, repo: string, input: CreatePullRequestInput): Promise<PullRequest> {
  return unwrap(await createPullRequest({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

export async function fetchPull(r: PullRef): Promise<PullRequest> {
  return unwrap(await getPullRequest(at(r)));
}

export async function fetchMergeability(r: PullRef): Promise<PullRequestMergeability> {
  return unwrap(await getPullRequestMergeability(at(r)));
}

export async function fetchPullReviews(r: PullRef): Promise<PullRequestReview[]> {
  return unwrap(await listPullRequestReviews({ ...at(r), query: { perPage: 100 } })).items;
}

export async function fetchPullComments(r: PullRef): Promise<PullRequestComment[]> {
  return unwrap(await listPullRequestComments({ ...at(r), query: { perPage: 100 } })).items;
}

export async function fetchPullCommits(r: PullRef): Promise<CommitSummary[]> {
  return unwrap(await listPullRequestCommits({ ...at(r), query: { perPage: 100 } })).items;
}

export async function fetchCommitStatus(owner: string, repo: string, sha: string): Promise<CombinedCommitStatus> {
  return unwrap(await getCombinedCommitStatus({ baseUrl: API_BASE_URL, path: { owner, repo, sha } }));
}

// Sintesi di piu' commit con una sola richiesta (max 100 sha, il tetto del contratto): sha -> stato.
export async function fetchCommitStatuses(owner: string, repo: string, shas: string[]): Promise<Record<string, CombinedCommitStatus['state']>> {
  const unique = [...new Set(shas)].slice(0, 100);
  if (unique.length === 0) return {};
  const res = unwrap(await listCombinedCommitStatuses({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { sha: unique } }));
  return Object.fromEntries(res.items.map((i) => [i.sha, i.state]));
}

export async function postPullComment(r: PullRef, body: string): Promise<PullRequestComment> {
  return unwrap(await createPullRequestComment({ ...at(r), body: { body } }));
}

export async function markReady(r: PullRef): Promise<PullRequest> {
  return unwrap(await markPullRequestReady(at(r)));
}

export async function markDraft(r: PullRef): Promise<PullRequest> {
  return unwrap(await markPullRequestDraft(at(r)));
}

export async function closePull(r: PullRef): Promise<PullRequest> {
  return unwrap(await closePullRequest(at(r)));
}

export async function reopenPull(r: PullRef): Promise<PullRequest> {
  return unwrap(await reopenPullRequest(at(r)));
}

export async function mergePull(r: PullRef, method: MergeMethod, deleteBranch: boolean, expectedHeadSha?: string): Promise<PullRequest> {
  return unwrap(await mergePullRequest({ ...at(r), body: { method, deleteBranch, ...(expectedHeadSha ? { expectedHeadSha } : {}) } }));
}

export async function deletePullBranch(r: PullRef): Promise<void> {
  unwrapEmpty(await deletePullRequestBranch(at(r)));
}

export async function requestReviewers(r: PullRef, reviewers: string[]): Promise<PullRequest> {
  return unwrap(await requestPullRequestReviewers({ ...at(r), body: { reviewers } }));
}

export async function dropReviewer(r: PullRef, username: string): Promise<PullRequest> {
  return unwrap(await removePullRequestReviewer({ baseUrl: API_BASE_URL, path: { owner: r.owner, repo: r.repo, number: r.number, username } }));
}

/** Diff della PR (B6):  `viewedPaths` porta i file gia' marcati Viewed da chi chiama. */
export async function fetchPullFiles(r: PullRef, ignoreWhitespace: boolean): Promise<PullRequestFiles> {
  return unwrap(await listPullRequestFiles({ ...at(r), query: ignoreWhitespace ? { ignoreWhitespace: true } : {} }));
}

export async function fetchPullThreads(r: PullRef): Promise<PullRequestThread[]> {
  return unwrap(await listPullRequestThreads({ ...at(r), query: { perPage: 100 } })).items;
}

/** Commento sulle righe (una o un gruppo, PR5) o risposta a una conversazione (`inReplyTo`). */
export async function postLineComment(r: PullRef, input: CreatePullRequestCommentInput): Promise<PullRequestComment> {
  return unwrap(await createPullRequestComment({ ...at(r), body: input }));
}

export async function submitReview(r: PullRef, event: ReviewEvent, body: string, commitSha?: string): Promise<PullRequestReview> {
  return unwrap(await createPullRequestReview({ ...at(r), body: { event, ...(body.trim() ? { body: body.trim() } : {}), ...(commitSha ? { commitSha } : {}) } }));
}

export async function setThreadResolved(r: PullRef, threadId: string, resolved: boolean): Promise<void> {
  const opts = { baseUrl: API_BASE_URL, path: { owner: r.owner, repo: r.repo, number: r.number, threadId } };
  unwrap(resolved ? await resolvePullRequestThread(opts) : await unresolvePullRequestThread(opts));
}

export async function setFileViewed(r: PullRef, path: string, viewed: boolean): Promise<void> {
  unwrapEmpty(await setPullRequestFileViewed({ ...at(r), body: { path, viewed } }));
}
