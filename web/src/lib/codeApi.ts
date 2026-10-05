// Letture del codice (M-04) via client generato (client di default: niente
// `client:` nelle options, cosi l'interceptor 401 vede le risposte).
import {
  getRepositoryBlame,
  getRepositoryBranches,
  getRepositoryCommit,
  getRepositoryCommits,
  getRepositoryFile,
  getRepositoryLanguages,
  getRepositoryReadme,
  getRepositoryTags,
  getRepositoryTree,
  searchRepositoryCode,
  listRepositoryFiles,
} from '@gitstack/api-client';
import type { Blame, BlameRange, Branch, CodeSearchResult, CommitDetail, CommitList, CommitSummary, FileDiff, FileContent, FileList, LanguageShare, Languages, Tag, Tree, TreeEntry } from '@gitstack/api-client';
import { API_BASE_URL, ApiError, unwrap } from './http';

export type { Blame, BlameRange, Branch, CommitDetail, CommitList, CommitSummary, FileDiff, FileContent, FileList, LanguageShare, Languages, Tag, Tree, TreeEntry };

export async function fetchTree(owner: string, repo: string, ref: string, path: string): Promise<Tree> {
  return unwrap(await getRepositoryTree({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref, ...(path ? { path } : {}) } }));
}

/** Il README della cartella, o null se non c'e' (404). */
export async function fetchReadme(owner: string, repo: string, ref: string, path: string): Promise<FileContent | null> {
  try {
    return unwrap(await getRepositoryReadme({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref, ...(path ? { path } : {}) } }));
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export async function fetchBranches(owner: string, repo: string): Promise<{ items: Branch[]; total: number }> {
  return unwrap(await getRepositoryBranches({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function fetchTags(owner: string, repo: string): Promise<{ items: Tag[]; total: number }> {
  return unwrap(await getRepositoryTags({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function fetchLanguages(owner: string, repo: string, ref: string): Promise<Languages> {
  return unwrap(await getRepositoryLanguages({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref } }));
}

export async function fetchFilePaths(owner: string, repo: string, ref: string): Promise<FileList> {
  return unwrap(await listRepositoryFiles({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref } }));
}

const encodePath = (p: string) => p.split('/').map(encodeURIComponent).join('/');

/** Percorso raw del contratto (B3): `<ref>/<percorso>` su piu' segmenti. */
export function rawUrl(owner: string, repo: string, ref: string, path: string): string {
  return `${API_BASE_URL}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/raw/${encodePath(ref)}/${encodePath(path)}`;
}

export function archiveUrl(owner: string, repo: string, ref: string, format: 'zip' | 'tar.gz'): string {
  return `${API_BASE_URL}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/archive?ref=${encodeURIComponent(ref)}&format=${format}`;
}

/** Pagina del browser: la radice del ref principale e' /<owner>/<repo>. */
export function treeHref(owner: string, repo: string, ref: string, path: string, defaultRef?: string): string {
  const base = `/${owner}/${repo}`;
  if (path === '' && ref === defaultRef) return base;
  return `${base}/tree/${encodePath(ref)}${path ? `/${encodePath(path)}` : ''}`;
}

export function blobHref(owner: string, repo: string, ref: string, path: string): string {
  return `/${owner}/${repo}/blob/${encodePath(ref)}/${encodePath(path)}`;
}

/**
 * `<ref>/<percorso>` dell'indirizzo: un ref puo' contenere `/`, quindi vince
 * il prefisso piu' lungo che e' un branch o un tag; altrimenti il primo segmento.
 */
export function splitRefPath(splat: string, refNames: readonly string[]): { ref: string; path: string } {
  const clean = splat.replace(/^\/+|\/+$/g, '');
  let best = '';
  for (const name of refNames) {
    if ((clean === name || clean.startsWith(`${name}/`)) && name.length > best.length) best = name;
  }
  if (best === '') {
    const i = clean.indexOf('/');
    best = i < 0 ? clean : clean.slice(0, i);
  }
  return { ref: best, path: clean.slice(best.length).replace(/^\//, '') };
}

// Il colore non e' nel contratto: mappa locale, grigio se il nome non c'e'.
const LANGUAGE_COLORS: Record<string, string> = {
  Go: '#00add8',
  TypeScript: '#3178c6',
  JavaScript: '#f1e05a',
  Python: '#3572a5',
  Shell: '#89e051',
  HTML: '#e34c26',
  CSS: '#563d7c',
  Java: '#b07219',
  Rust: '#dea584',
  Ruby: '#701516',
  PHP: '#4f5d95',
  C: '#555555',
  'C++': '#f34b7d',
  'C#': '#178600',
  Dockerfile: '#384d54',
  Makefile: '#427819',
  HCL: '#844fba',
  Smarty: '#d9b400',
  PLpgSQL: '#336790',
  SQL: '#e38c00',
};
export const FALLBACK_LANGUAGE_COLOR = '#8b949e';

export function languageColor(name: string): string {
  return LANGUAGE_COLORS[name] ?? FALLBACK_LANGUAGE_COLOR;
}

/**
 * Corrispondenza approssimata per «Go to file» (B5): i caratteri della query
 * devono comparire in ordine nel percorso. Punteggio piu' alto = meglio:
 * premia sottostringhe contigue, inizi di segmento e il nome del file.
 */
export function fuzzyScore(query: string, path: string): number | null {
  const q = query.toLowerCase().replace(/\s+/g, '');
  if (q === '') return 0;
  const p = path.toLowerCase();
  const base = p.slice(p.lastIndexOf('/') + 1);
  let score = 0;
  if (p.includes(q)) score += 100 - Math.min(50, p.indexOf(q));
  if (base.includes(q)) score += 60;
  if (base.startsWith(q)) score += 30;
  let at = -1;
  let prev = -2;
  for (const ch of q) {
    at = p.indexOf(ch, at + 1);
    if (at < 0) return null;
    if (at === prev + 1) score += 5;
    if (at === 0 || '/._-'.includes(p[at - 1] ?? '')) score += 3;
    prev = at;
  }
  return score - path.length / 100;
}

export function fuzzyFilter(query: string, paths: readonly string[], limit = 50): string[] {
  const scored: { path: string; score: number }[] = [];
  for (const path of paths) {
    const score = fuzzyScore(query, path);
    if (score !== null) scored.push({ path, score });
  }
  scored.sort((a, b) => b.score - a.score || a.path.localeCompare(b.path));
  return scored.slice(0, limit).map((s) => s.path);
}

export async function fetchFile(owner: string, repo: string, ref: string, path: string): Promise<FileContent> {
  return unwrap(await getRepositoryFile({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref, path } }));
}

export async function fetchBlame(owner: string, repo: string, ref: string, path: string): Promise<Blame> {
  return unwrap(await getRepositoryBlame({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { ref, path } }));
}

/** Indirizzo pubblico del raw (B3): /<owner>/<repo>/raw/<ref>/<percorso>. */
export function rawHref(owner: string, repo: string, ref: string, path: string): string {
  return `/${owner}/${repo}/raw/${encodePath(ref)}/${encodePath(path)}`;
}

export function blameHref(owner: string, repo: string, ref: string, path: string): string {
  return `/${owner}/${repo}/blame/${encodePath(ref)}/${encodePath(path)}`;
}

/** Storico filtrato sul file (B4, GIT-88): /<owner>/<repo>/commits/<ref>/<percorso>. */
export function historyHref(owner: string, repo: string, ref: string, path: string): string {
  return `/${owner}/${repo}/commits/${encodePath(ref)}/${encodePath(path)}`;
}

export function commitHref(owner: string, repo: string, sha: string): string {
  return `/${owner}/${repo}/commit/${sha}`;
}

/** Contenuto testuale di un file: UTF-8 diretto o base64 decodificato; null se manca o non si decodifica. */
export function decodeContent(f: FileContent): string | null {
  if (f.content === undefined) return null;
  if (f.encoding !== 'base64') return f.content;
  try {
    const bin = atob(f.content);
    return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
  } catch {
    return null;
  }
}

/** Nome lowlight dal nome del file; undefined se non riconosciuto. */
const EXT_LANG: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  py: 'python', sh: 'bash', bash: 'bash', json: 'json', yml: 'yaml', yaml: 'yaml', css: 'css', html: 'xml', xml: 'xml',
  sql: 'sql', md: 'markdown', java: 'java', rs: 'rust', rb: 'ruby', php: 'php', c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp',
  cs: 'csharp', diff: 'diff', patch: 'diff', ini: 'ini', toml: 'ini', mod: 'go', kt: 'kotlin', swift: 'swift',
};
export function languageFor(fileName: string): string | undefined {
  const lower = fileName.toLowerCase();
  if (lower === 'makefile') return 'makefile';
  if (lower === 'dockerfile') return 'dockerfile';
  const i = lower.lastIndexOf('.');
  return i < 0 ? undefined : EXT_LANG[lower.slice(i + 1)];
}

export function isMarkdownName(fileName: string): boolean {
  return /\.(md|markdown)$/i.test(fileName);
}

export const COMMITS_PER_PAGE = 30;

export async function fetchCommits(owner: string, repo: string, q: { ref: string; author?: string; path?: string; page?: number }): Promise<CommitList> {
  return unwrap(
    await getRepositoryCommits({
      baseUrl: API_BASE_URL,
      path: { owner, repo },
      query: { ref: q.ref, perPage: COMMITS_PER_PAGE, ...(q.author ? { author: q.author } : {}), ...(q.path ? { path: q.path } : {}), ...(q.page && q.page > 1 ? { page: q.page } : {}) },
    }),
  );
}

export async function fetchCommit(owner: string, repo: string, sha: string, ignoreWhitespace: boolean): Promise<CommitDetail> {
  return unwrap(await getRepositoryCommit({ baseUrl: API_BASE_URL, path: { owner, repo, sha }, query: ignoreWhitespace ? { ignoreWhitespace: true } : {} }));
}

/** Download del diff intero (B6): .diff o .patch, senza limiti. */
export function commitDownloadUrl(owner: string, repo: string, sha: string, format: "diff" | "patch", ignoreWhitespace = false): string {
  return `${API_BASE_URL}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/commits/${encodeURIComponent(sha)}/patch?format=${format}${ignoreWhitespace ? "&ignoreWhitespace=true" : ""}`;
}

/** Storico dalla radice (tab Commits): /<owner>/<repo>/commits/<ref>. */
export function commitsHref(owner: string, repo: string, ref: string, path = ""): string {
  return `/${owner}/${repo}/commits/${encodePath(ref)}${path ? `/${encodePath(path)}` : ""}`;
}

export type { CodeSearchHit, CodeSearchResult } from '@gitstack/api-client';

/** Search code (B5): sottostringa letterale sul ref, al massimo 100 risultati. */
export async function searchCode(owner: string, repo: string, q: string, ref: string): Promise<CodeSearchResult> {
  return unwrap(await searchRepositoryCode({ baseUrl: API_BASE_URL, path: { owner, repo }, query: { q, ref } }));
}

/** Pagina dei risultati: /<owner>/<repo>/search?q=&ref=. */
export function searchHref(owner: string, repo: string, q: string, ref: string): string {
  return `/${owner}/${repo}/search?q=${encodeURIComponent(q)}&ref=${encodeURIComponent(ref)}`;
}

/** Vista file alla riga della corrispondenza (#L<n>). */
export function blobLineHref(owner: string, repo: string, ref: string, path: string, line: number): string {
  return `${blobHref(owner, repo, ref, path)}#L${line}`;
}

export function tagsHref(owner: string, repo: string): string {
  return `/${owner}/${repo}/tags`;
}
