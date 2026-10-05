// Repository (M-03) via client generato (client di default: niente `client:`
// nelle options, cosi l'interceptor 401 vede le risposte).
import {
  createRepository,
  deleteRepository,
  getRepository,
  listDeletedRepositories,
  listRepositories,
  restoreRepository,
  updateRepository,
} from '@gitstack/api-client';
import type {
  CreateRepositoryInput,
  DeletedRepository,
  GitignoreTemplate,
  LicenseTemplate,
  OwnerType,
  RepoVisibility,
  Repository,
  UpdateRepositoryInput,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CreateRepositoryInput, DeletedRepository, UpdateRepositoryInput, GitignoreTemplate, LicenseTemplate, OwnerType, RepoVisibility, Repository };

// Id stabili dei modelli (D-D, uguali a GIT-69).
export const GITIGNORE_TEMPLATES = [
  'go',
  'node',
  'python',
  'java',
  'dotnet',
  'rust',
  'cpp',
  'terraform',
  'ruby',
  'php',
] as const satisfies readonly GitignoreTemplate[];
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
] as const satisfies readonly LicenseTemplate[];

const PER_PAGE = 100;

export async function fetchRepos(owner?: string): Promise<Repository[]> {
  return unwrap(await listRepositories({ baseUrl: API_BASE_URL, query: { perPage: PER_PAGE, ...(owner ? { owner } : {}) } })).items;
}

export async function fetchRepo(owner: string, repo: string): Promise<Repository> {
  return unwrap(await getRepository({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function createRepo(input: CreateRepositoryInput): Promise<Repository> {
  return unwrap(await createRepository({ baseUrl: API_BASE_URL, body: input }));
}

export async function updateRepo(owner: string, repo: string, input: UpdateRepositoryInput): Promise<Repository> {
  return unwrap(await updateRepository({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

export async function deleteRepo(owner: string, repo: string): Promise<void> {
  unwrapEmpty(await deleteRepository({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function fetchDeletedRepos(owner?: string): Promise<DeletedRepository[]> {
  return unwrap(await listDeletedRepositories({ baseUrl: API_BASE_URL, ...(owner ? { query: { owner } } : {}) })).items;
}

export async function restoreRepo(repoId: string): Promise<Repository> {
  return unwrap(await restoreRepository({ baseUrl: API_BASE_URL, path: { repoId } }));
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
