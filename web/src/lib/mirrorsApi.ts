// Mirror in push di un repo (V8, GIT-179) via client generato (client di
// default: niente `client:` nelle options, cosi l'interceptor 401 vede le risposte).
import {
  createRepoMirror,
  deleteRepoMirror,
  listRepoMirrorRuns,
  listRepoMirrors,
  syncRepoMirror,
  updateRepoMirror,
} from '@gitstack/api-client';
import type { RepoMirror, RepoMirrorRun, RepoMirrorRunOutcome, RepoMirrorState } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { RepoMirror, RepoMirrorRun, RepoMirrorRunOutcome, RepoMirrorState };

const PER_PAGE = 100;
const RUNS = 10;

export async function fetchMirrors(owner: string, repo: string): Promise<RepoMirror[]> {
  return unwrap(await listRepoMirrors({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { perPage: PER_PAGE } })).items;
}

export async function fetchMirrorRuns(owner: string, repo: string, mirrorId: string): Promise<RepoMirrorRun[]> {
  return unwrap(await listRepoMirrorRuns({ baseUrl: API_BASE_URL, path: { owner, repo, mirrorId }, query: { perPage: RUNS } })).items;
}

export async function addMirror(
  owner: string,
  repo: string,
  input: { url: string; username: string; token: string; enabled?: boolean },
): Promise<RepoMirror> {
  return unwrap(await createRepoMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

// Il token si puo' solo sostituire: se assente non viene inviato.
export async function editMirror(
  owner: string,
  repo: string,
  mirrorId: string,
  input: { url?: string; username?: string; token?: string; enabled?: boolean },
): Promise<RepoMirror> {
  return unwrap(await updateRepoMirror({ baseUrl: API_BASE_URL, path: { owner, repo, mirrorId }, body: input }));
}

export async function removeMirror(owner: string, repo: string, mirrorId: string): Promise<void> {
  unwrapEmpty(await deleteRepoMirror({ baseUrl: API_BASE_URL, path: { owner, repo, mirrorId } }));
}

export async function syncMirror(owner: string, repo: string, mirrorId: string): Promise<RepoMirror> {
  return unwrap(await syncRepoMirror({ baseUrl: API_BASE_URL, path: { owner, repo, mirrorId } }));
}
