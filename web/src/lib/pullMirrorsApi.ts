// Mirror in pull di un repo (M-09) via client generato (client di default:
// niente `client:` nelle options, quindi l'interceptor 401 vede le risposte).
import {
  enableRepoPullMirror,
  getRepoPullMirror,
  listRepoPullMirrorRuns,
  pauseRepoPullMirror,
  resumeRepoPullMirror,
  stopRepoPullMirror,
  syncRepoPullMirror,
  updateRepoPullMirror,
} from '@gitstack/api-client';
import type { PullMirror, PullMirrorRun } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { PullMirror, PullMirrorRun };

const RUNS = 50;

export async function fetchPullMirror(owner: string, repo: string): Promise<PullMirror | null> {
  try {
    return await unwrap(await getRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
  } catch {
    return null;
  }
}

export async function fetchPullMirrorRuns(owner: string, repo: string): Promise<PullMirrorRun[]> {
  return unwrap(await listRepoPullMirrorRuns({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { perPage: RUNS } })).items;
}

export async function enablePullMirror(
  owner: string,
  repo: string,
  input: { url: string; username?: string; intervalSeconds?: number },
): Promise<PullMirror> {
  return unwrap(
    await enableRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }),
  );
}

export async function updatePullMirror(
  owner: string,
  repo: string,
  input: { intervalSeconds?: number; username?: string; token?: string },
): Promise<PullMirror> {
  return unwrap(
    await updateRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }),
  );
}

export async function pausePullMirror(owner: string, repo: string): Promise<PullMirror> {
  return unwrap(
    await pauseRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }),
  );
}

export async function resumePullMirror(owner: string, repo: string): Promise<PullMirror> {
  return unwrap(
    await resumeRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }),
  );
}

export async function stopPullMirror(owner: string, repo: string, confirm: string): Promise<PullMirror> {
  return unwrap(
    await stopRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: { confirm } }),
  );
}

export async function syncPullMirror(owner: string, repo: string): Promise<PullMirror> {
  return unwrap(
    await syncRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }),
  );
}
