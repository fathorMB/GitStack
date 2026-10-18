// Pull request (v1.1) via client generato (client di default: l'interceptor 401 le vede).
import {
  closePullRequest,
  createPullRequest,
  createPullRequestComment,
  deletePullRequestBranch,
  getCombinedCommitStatus,
  getPullRequest,
  getPullRequestMergeability,
  listPullRequestCommits,
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
  CreatePullRequestInput,
  MergeMethod,
  PullRequest,
  PullRequestComment,
  PullRequestList,
  PullRequestMergeability,
  PullRequestReview,
  PullRequestSummary,
  CommitSummary,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CombinedCommitStatus, CreatePullRequestInput, MergeMethod, PullRequest, PullRequestComment, PullRequestList, PullRequestMergeability, PullRequestReview, PullRequestSummary };

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
