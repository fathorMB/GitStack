// Release (REL1, mockup B7): avvolge le operazioni generate di @gitstack/api-client.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  createRelease as sdkCreateRelease,
  deleteRelease as sdkDeleteRelease,
  listReleases as sdkListReleases,
  updateRelease as sdkUpdateRelease,
  uploadReleaseAsset as sdkUploadReleaseAsset,
} from '@gitstack/api-client';
import type { CreateReleaseInput, Release, ReleaseAsset, ReleaseList, UpdateReleaseInput } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CreateReleaseInput, Release, ReleaseAsset, ReleaseList, UpdateReleaseInput };

export async function fetchReleases(owner: string, repo: string): Promise<ReleaseList> {
  return unwrap(await sdkListReleases({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function createRelease(owner: string, repo: string, input: CreateReleaseInput): Promise<Release> {
  return unwrap(await sdkCreateRelease({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

export async function updateRelease(owner: string, repo: string, releaseId: string, input: UpdateReleaseInput): Promise<Release> {
  return unwrap(await sdkUpdateRelease({ baseUrl: API_BASE_URL, path: { owner, repo, releaseId }, body: input }));
}

export async function deleteRelease(owner: string, repo: string, releaseId: string): Promise<void> {
  unwrapEmpty(await sdkDeleteRelease({ baseUrl: API_BASE_URL, path: { owner, repo, releaseId } }));
}

export async function uploadReleaseAsset(owner: string, repo: string, releaseId: string, file: File): Promise<ReleaseAsset> {
  return unwrap(
    await sdkUploadReleaseAsset({
      baseUrl: API_BASE_URL,
      path: { owner, repo, releaseId },
      query: { name: file.name },
      body: file,
      headers: { 'Content-Type': 'application/octet-stream' },
    }),
  );
}

/** Indirizzo di scaricamento di un file allegato (sessione o token, come gli archivi). */
export function releaseAssetUrl(owner: string, repo: string, releaseId: string, assetId: string): string {
  const e = encodeURIComponent;
  return `${API_BASE_URL}/repos/${e(owner)}/${e(repo)}/releases/${e(releaseId)}/assets/${e(assetId)}/download`;
}

/** Hash corto come nel mockup: sha256:1f2e…9a. */
export function shortSha(sha: string): string {
  const h = sha.replace(/^sha256:/, '');
  return h.length > 8 ? `sha256:${h.slice(0, 4)}…${h.slice(-2)}` : `sha256:${h}`;
}
