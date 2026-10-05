// Repository (M-03, decisione D-D di GIT-63). Finche il client generato non
// ha le operazioni `repos`, i tipi sono locali e le chiamate passano dal
// client di default dell'SDK (stessa forma { data, error, response }): cosi
// l'interceptor 401 di unauthorized.ts le vede. Da sostituire con l'SDK
// quando GIT-63 e' su main.
import { client } from '@gitstack/api-client/src/generated/client.gen';
import { API_BASE_URL, unwrap } from './http';

export type RepoVisibility = 'private' | 'internal';
export type OwnerType = 'user' | 'organization';

// Id stabili dei modelli (D-D, uguali a GIT-69).
export const GITIGNORE_TEMPLATES = ['go', 'node', 'python', 'java', 'dotnet', 'rust', 'cpp', 'terraform', 'ruby', 'php'] as const;
export const LICENSE_TEMPLATES = [
  'mit',
  'apache-2.0',
  'gpl-3.0',
  'agpl-3.0',
  'lgpl-3.0',
  'mpl-2.0',
  'bsd-2-clause',
  'bsd-3-clause',
  'unlicense',
] as const;
export type GitignoreTemplate = (typeof GITIGNORE_TEMPLATES)[number];
export type LicenseTemplate = (typeof LICENSE_TEMPLATES)[number];

export interface Repository {
  id: string;
  owner: { type: OwnerType; name: string };
  name: string;
  fullName: string;
  description?: string;
  visibility: RepoVisibility;
  defaultBranch: string;
  protectDefaultBranch: boolean;
  archived: boolean;
  archivedAt?: string | null;
  empty: boolean;
  cloneUrls: { https: string; ssh: string };
  createdAt: string;
  updatedAt: string;
}

export interface RepositoryList {
  items: Repository[];
  page?: number;
  perPage?: number;
  total?: number;
}

export interface CreateRepositoryInput {
  owner: string;
  name: string;
  description?: string;
  visibility?: RepoVisibility;
  readme?: boolean;
  gitignoreTemplate?: GitignoreTemplate;
  licenseTemplate?: LicenseTemplate;
}

const PER_PAGE = 100;

export async function fetchRepos(owner?: string): Promise<Repository[]> {
  const result = await client.get<RepositoryList, never>({
    baseUrl: API_BASE_URL,
    url: '/repos',
    query: { perPage: PER_PAGE, ...(owner ? { owner } : {}) },
  });
  return unwrap(result as { data?: RepositoryList }).items;
}

export async function fetchRepo(owner: string, repo: string): Promise<Repository> {
  const result = await client.get<Repository, never>({
    baseUrl: API_BASE_URL,
    url: '/repos/{owner}/{repo}',
    path: { owner, repo },
  });
  return unwrap(result as { data?: Repository });
}

export async function createRepo(input: CreateRepositoryInput): Promise<Repository> {
  const result = await client.post<Repository, never>({
    baseUrl: API_BASE_URL,
    url: '/repos',
    body: input,
    headers: { 'Content-Type': 'application/json' },
  });
  return unwrap(result as { data?: Repository });
}

// R11: stessa regola del backend (D-A) piu «non finisce con .git».
// Un messaggio per regola; stringa vuota se il nome e' valido.
export function validateRepoName(name: string): string {
  if (name === '') return 'Enter a name.';
  if (name.length > 100) return 'Use at most 100 characters.';
  if (!/^[a-z0-9_-]/.test(name)) return 'Start with a lowercase letter, a digit, "-" or "_".';
  if (!/^[a-z0-9._-]+$/.test(name)) return 'Use only lowercase letters, digits, "-", "_" and ".".';
  if (name.endsWith('.git')) return 'The name cannot end with ".git".';
  return '';
}
