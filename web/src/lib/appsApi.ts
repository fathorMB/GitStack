// Apps del repo (v3-a, regole H1-H7 e mockup H1-H7): operazioni generate di @gitstack/api-client.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  createApp as sdkCreateApp,
  deployApp as sdkDeployApp,
  getApp as sdkGetApp,
  getAppAccess as sdkGetAppAccess,
  addAppAccessMember as sdkAddAppAccessMember,
  getAppAccessInvite as sdkGetAppAccessInvite,
  listAppAccessMembers as sdkListAppAccessMembers,
  removeAppAccessMember as sdkRemoveAppAccessMember,
  getAppMetrics as sdkGetAppMetrics,
  getAppLogs as sdkGetAppLogs,
  listAppSecrets as sdkListAppSecrets,
  listAppVariables as sdkListAppVariables,
  listAppVersions as sdkListAppVersions,
  listApps as sdkListApps,
  restartApp as sdkRestartApp,
  rollbackApp as sdkRollbackApp,
  scaleApp as sdkScaleApp,
  setAppAccess as sdkSetAppAccess,
  updateApp as sdkUpdateApp,
  putAppSecret as sdkPutAppSecret,
  putAppVariable as sdkPutAppVariable,
  deleteAppSecret as sdkDeleteAppSecret,
  deleteAppVariable as sdkDeleteAppVariable,
  deleteApp as sdkDeleteApp,
} from '@gitstack/api-client';
import type {
  App,
  AppAccess,
  AppAccessInvite,
  AppAccessMember,
  AppAccessMode,
  AppCreateInput,
  AppCreated,
  AppList,
  AppMetrics,
  AppUpdateInput,
  AppVersion,
  AppVersionList,
  AppLogLine,
  AppSecretList,
  AppVariableList,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { App, AppAccess, AppAccessInvite, AppAccessMember, AppAccessMode, AppCreateInput, AppCreated, AppList, AppMetrics, AppUpdateInput, AppVersion, AppVersionList, AppLogLine, AppSecretList, AppVariableList };

const at = (owner: string, repo: string) => ({ baseUrl: API_BASE_URL, path: { owner, repo } });
const atApp = (owner: string, repo: string, app: string) => ({ baseUrl: API_BASE_URL, path: { owner, repo, app } });

export async function fetchApps(owner: string, repo: string): Promise<AppList> {
  return unwrap(await sdkListApps(at(owner, repo)));
}

/** Numero di app del repo (contatore della scheda). */
export async function countApps(owner: string, repo: string): Promise<number> {
  return (await fetchApps(owner, repo)).items.length;
}

export async function fetchApp(owner: string, repo: string, app: string): Promise<App> {
  return unwrap(await sdkGetApp(atApp(owner, repo, app)));
}

export async function createApp(owner: string, repo: string, input: AppCreateInput): Promise<AppCreated> {
  return unwrap(await sdkCreateApp({ ...at(owner, repo), body: input }));
}

export async function updateApp(owner: string, repo: string, app: string, input: AppUpdateInput): Promise<App> {
  return unwrap(await sdkUpdateApp({ ...atApp(owner, repo, app), body: input }));
}

export async function deployApp(owner: string, repo: string, app: string, image: string): Promise<AppVersion> {
  return unwrap(await sdkDeployApp({ ...atApp(owner, repo, app), body: { image } }));
}

export async function restartApp(owner: string, repo: string, app: string): Promise<void> {
  unwrapEmpty(await sdkRestartApp(atApp(owner, repo, app)));
}

export async function scaleApp(owner: string, repo: string, app: string, copies: number): Promise<App> {
  return unwrap(await sdkScaleApp({ ...atApp(owner, repo, app), body: { copies } }));
}

export async function fetchAppVersions(owner: string, repo: string, app: string): Promise<AppVersionList> {
  return unwrap(await sdkListAppVersions({ ...atApp(owner, repo, app), query: { perPage: 50 } }));
}

export async function rollbackApp(owner: string, repo: string, app: string, version: number, pauseAutoDeploy: boolean): Promise<AppVersion> {
  return unwrap(await sdkRollbackApp({ ...atApp(owner, repo, app), body: { version, pauseAutoDeploy } }));
}

export async function fetchAppMetrics(owner: string, repo: string, app: string): Promise<AppMetrics> {
  return unwrap(await sdkGetAppMetrics({ ...atApp(owner, repo, app), query: { range: '6h' } }));
}

export async function fetchAppLogs(owner: string, repo: string, app: string, options: { since?: string; copy?: string; source?: 'all' | 'app' | 'platform'; grep?: string } = {}): Promise<AppLogLine[]> {
  const result = await sdkGetAppLogs({ ...atApp(owner, repo, app), query: { ...options, format: 'ndjson', limit: 1000 }, parseAs: 'text' });
  const text = unwrap(result) as unknown as string;
  return text.split('\n').filter(Boolean).map((line) => JSON.parse(line) as AppLogLine);
}

export async function fetchAppVariables(owner: string, repo: string, app: string): Promise<AppVariableList> {
  return unwrap(await sdkListAppVariables(atApp(owner, repo, app)));
}

export async function fetchAppSecrets(owner: string, repo: string, app: string): Promise<AppSecretList> {
  return unwrap(await sdkListAppSecrets(atApp(owner, repo, app)));
}

export async function saveAppVariable(owner: string, repo: string, app: string, key: string, value: string): Promise<void> {
  unwrap(await sdkPutAppVariable({ ...atApp(owner, repo, app), path: { ...atApp(owner, repo, app).path, name: key }, body: { value } }));
}

export async function saveAppSecret(owner: string, repo: string, app: string, key: string, value: string): Promise<void> {
  unwrap(await sdkPutAppSecret({ ...atApp(owner, repo, app), path: { ...atApp(owner, repo, app).path, name: key }, body: { value } }));
}

export async function removeAppVariable(owner: string, repo: string, app: string, key: string): Promise<void> {
  unwrapEmpty(await sdkDeleteAppVariable({ ...atApp(owner, repo, app), path: { ...atApp(owner, repo, app).path, name: key } }));
}

export async function removeAppSecret(owner: string, repo: string, app: string, key: string): Promise<void> {
  unwrapEmpty(await sdkDeleteAppSecret({ ...atApp(owner, repo, app), path: { ...atApp(owner, repo, app).path, name: key } }));
}

export async function removeApp(owner: string, repo: string, app: string): Promise<void> {
  unwrapEmpty(await sdkDeleteApp(atApp(owner, repo, app)));
}

export async function fetchAppAccess(owner: string, repo: string, app: string): Promise<AppAccess> {
  return unwrap(await sdkGetAppAccess(atApp(owner, repo, app)));
}

export async function saveAppAccess(owner: string, repo: string, app: string, mode: AppAccessMode): Promise<AppAccess> {
  return unwrap(await sdkSetAppAccess({ ...atApp(owner, repo, app), body: { mode } }));
}

// ---- lista di accesso dell'app (H6, GIT-376) ----

export async function fetchAppAccessMembers(owner: string, repo: string, app: string): Promise<AppAccessMember[]> {
  return unwrap(await sdkListAppAccessMembers(atApp(owner, repo, app))).items;
}

export async function addAppAccessMember(owner: string, repo: string, app: string, kind: 'user' | 'team', name: string): Promise<AppAccessMember> {
  return unwrap(await sdkAddAppAccessMember({ ...atApp(owner, repo, app), body: { kind, name } }));
}

export async function removeAppAccessMember(owner: string, repo: string, app: string, memberId: string): Promise<void> {
  unwrapEmpty(await sdkRemoveAppAccessMember({ ...atApp(owner, repo, app), path: { ...atApp(owner, repo, app).path, memberId } }));
}

/** Invito ai tester: testo, link alla guida CA e impronta arrivano dall'API, il client non li compone. */
export async function fetchAppAccessInvite(owner: string, repo: string, app: string): Promise<AppAccessInvite> {
  return unwrap(await sdkGetAppAccessInvite(atApp(owner, repo, app)));
}
