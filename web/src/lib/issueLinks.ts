import type { IssueEvent } from './issuesApi';

/** Commit collegato a una issue (eventi `commit_linked` e `closed_by_commit`, M-06 C2). */
export interface LinkedCommit {
  sha: string;
  repository: string;
  subject: string;
  ref: string;
  authorName: string;
}

/** Riferimento da un'altra issue (evento `referenced_from`, M-06 C1). */
export interface ReferenceSource {
  kind: string;
  repository: string;
  number: number;
  title: string;
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '');

export function commitOf(e: IssueEvent): LinkedCommit | null {
  const c = (e.data as Record<string, unknown> | undefined)?.commit;
  if (!c || typeof c !== 'object') return null;
  const o = c as Record<string, unknown>;
  const sha = str(o.sha);
  if (!sha) return null;
  return { sha, repository: str(o.repository), subject: str(o.subject), ref: str(o.ref), authorName: str(o.authorName) };
}

export function sourceOf(e: IssueEvent): ReferenceSource | null {
  const s = (e.data as Record<string, unknown> | undefined)?.source;
  if (!s || typeof s !== 'object') return null;
  const o = s as Record<string, unknown>;
  const number = typeof o.number === 'number' ? o.number : Number(o.number);
  if (!Number.isInteger(number) || !str(o.repository)) return null;
  return { kind: str(o.kind) || 'issue', repository: str(o.repository), number, title: str(o.title) };
}

/** `refs/heads/main` -> `main`. */
export function branchName(ref: string): string {
  return ref.replace(/^refs\/heads\//, '');
}

export const shortSha = (sha: string): string => sha.slice(0, 7);

/** Commit distinti (per sha) collegati alla issue, nell'ordine della cronologia. */
export function linkedCommits(events: IssueEvent[]): LinkedCommit[] {
  const seen = new Set<string>();
  const out: LinkedCommit[] = [];
  for (const e of events) {
    if (e.type !== 'commit_linked' && e.type !== 'closed_by_commit') continue;
    const c = commitOf(e);
    if (!c || seen.has(c.sha)) continue;
    seen.add(c.sha);
    out.push(c);
  }
  return out;
}

export interface CommitGroup {
  id: string;
  at: string;
  actor: string;
  commits: LinkedCommit[];
}

const GROUP_WINDOW_MS = 60_000;

/** Raggruppa i `commit_linked` dello stesso push (stesso attore, entro un minuto) in «pushed N commits». */
export function groupCommitEvents(events: IssueEvent[]): CommitGroup[] {
  const sorted = events
    .filter((e) => e.type === 'commit_linked' && commitOf(e))
    .sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
  const groups: CommitGroup[] = [];
  for (const e of sorted) {
    const c = commitOf(e) as LinkedCommit;
    const actor = e.actor?.username ?? c.authorName;
    const last = groups[groups.length - 1];
    if (last && last.actor === actor && Date.parse(e.createdAt) - Date.parse(last.at) <= GROUP_WINDOW_MS) {
      last.commits.push(c);
    } else {
      groups.push({ id: e.id, at: e.createdAt, actor, commits: [c] });
    }
  }
  return groups;
}
