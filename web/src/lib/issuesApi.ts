// Issues (M-05) via client generato (client di default: l'interceptor 401 le vede).
import { listIssues, listLabels, listMilestones } from '@gitstack/api-client';
import type { IssueList, IssueSummary, IssueUser, Label, LabelList, Milestone, MilestoneList } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { IssueList, IssueSummary, IssueUser, Label, Milestone };

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
