import { ApiError } from './http';

// Indice dei download servito dall'istanza: GET /downloads/index.json
// (formato fissato con GIT-171, vedi README di web, sezione M-07).
export interface DownloadBinary {
  os: 'linux' | 'darwin' | 'windows';
  arch: 'amd64' | 'arm64';
  file: string;
  url: string;
  sha256: string;
  size: number;
}

export interface DownloadsIndex {
  version: string;
  binaries: DownloadBinary[];
  checksums: string;
  skills: { file: string; url: string; sha256: string } | null;
  install: { sh: string; ps1: string };
  ca_cert: string | null;
}

function fail(message: string, status = 502): ApiError {
  return new ApiError({ error: { code: "downloads_index", message } }, status);
}

export const DOWNLOADS_INDEX_URL = '/downloads/index.json';

// Errore con un messaggio mostrabile: l'indice puo' mancare (404 finche' GIT-171
// non e' sull'immagine web) o essere in un formato inatteso.
export async function fetchDownloadsIndex(): Promise<DownloadsIndex> {
  let res: Response;
  try {
    res = await fetch(DOWNLOADS_INDEX_URL, { headers: { Accept: 'application/json' } });
  } catch {
    throw fail('Could not reach the server.');
  }
  if (!res.ok) throw fail(`The downloads index is not available (HTTP ${res.status}).`, res.status);
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    throw fail('The downloads index is not valid JSON.');
  }
  const idx = body as Partial<DownloadsIndex> | null;
  if (!idx || typeof idx.version !== 'string' || !Array.isArray(idx.binaries)) {
    throw fail('The downloads index has an unexpected format.');
  }
  return {
    version: idx.version,
    binaries: idx.binaries,
    checksums: idx.checksums ?? '/downloads/SHA256SUMS',
    skills: idx.skills ?? null,
    install: idx.install ?? { sh: '/install-gs.sh', ps1: '/install-gs.ps1' },
    ca_cert: idx.ca_cert ?? null,
  };
}
