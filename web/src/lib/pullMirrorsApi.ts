// Mirror in pull (MP1-MP8, GIT-196) via client generato (client di default:
// niente `client:` nelle options, cosi l'interceptor 401 vede le risposte).
import {
  enableRepoPullMirror,
  getRepoPullMirror,
  importRepository,
  listRepoPullMirrorRuns,
  pauseRepoPullMirror,
  resumeRepoPullMirror,
  stopRepoPullMirror,
  syncRepoPullMirror,
  updateRepoPullMirror,
} from '@gitstack/api-client';
import type {
  EnablePullMirrorInput,
  ImportRepositoryInput,
  PullMirrorRun,
  PullMirrorRunList,
  UpdatePullMirrorInput,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { EnablePullMirrorInput, ImportRepositoryInput, PullMirrorRun, UpdatePullMirrorInput };

/** Crea un repo nuovo importandolo da un'origine remota (MP3, MP8). */
export async function importRepo(input: ImportRepositoryInput): Promise<void> {
  unwrapEmpty(await importRepository({ baseUrl: API_BASE_URL, body: input }));
}

/** Attiva il mirror in pull su un repo gia' esistente. */
export async function enablePullMirror(owner: string, repo: string, input: EnablePullMirrorInput): Promise<void> {
  unwrapEmpty(
    await enableRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }),
  );
}

/** Legge i dettagli completi del mirror in pull. */
export async function fetchPullMirror(owner: string, repo: string) {
  return unwrap(await getRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

/** Aggiorna le opzioni del mirror (intervallo, username). */
export async function editPullMirror(
  owner: string,
  repo: string,
  input: UpdatePullMirrorInput,
): Promise<void> {
  unwrapEmpty(await updateRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

/** Sincronizza subito il mirror. */
export async function syncPullMirror(owner: string, repo: string): Promise<void> {
  unwrapEmpty(await syncRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

/** Mette in pausa il mirror. */
export async function pausePullMirror(owner: string, repo: string): Promise<void> {
  unwrapEmpty(await pauseRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

/** Riprende il mirror dalla pausa. */
export async function resumePullMirror(owner: string, repo: string): Promise<void> {
  unwrapEmpty(await resumeRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

/** Interrompe definitivamente il mirror (richiede conferma col nome del repo). */
export async function stopPullMirror(owner: string, repo: string, name: string): Promise<void> {
  unwrapEmpty(
    await stopRepoPullMirror({ baseUrl: API_BASE_URL, path: { owner, repo }, body: { confirm: name } }),
  );
}

/** Elenco delle esecuzioni del mirror, ultime 10. */
export async function fetchPullMirrorRuns(owner: string, repo: string): Promise<PullMirrorRun[]> {
  return (
    (unwrap(await listRepoPullMirrorRuns({ baseUrl: API_BASE_URL, path: { owner, repo } })) as PullMirrorRunList)
      .items
  );
}
