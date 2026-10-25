// Apps del repo (v3-a, regole H1-H7 e mockup H1-H7): operazioni generate di @gitstack/api-client.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  createApp as sdkCreateApp,
  deployApp as sdkDeployApp,
  getApp as sdkGetApp,
  getAppAccess as sdkGetAppAccess,
  getAppMetrics as sdkGetAppMetrics,
  listAppVersions as sdkListAppVersions,
  listApps as sdkListApps,
  restartApp as sdkRestartApp,
  rollbackApp as sdkRollbackApp,
  scaleApp as sdkScaleApp,
  setAppAccess as sdkSetAppAccess,
  updateApp as sdkUpdateApp,
} from '@gitstack/api-client';
import type {
  App,
  AppAccess,
  AppAccessMode,
  AppCreateInput,
  AppCreated,
  AppList,
  AppMetrics,
  AppUpdateInput,
  AppVersion,
  AppVersionList,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { App, AppAccess, AppAccessMode, AppCreateInput, AppCreated, AppList, AppMetrics, AppUpdateInput, AppVersion, AppVersionList };

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

export async function fetchAppAccess(owner: string, repo: string, app: string): Promise<AppAccess> {
  return unwrap(await sdkGetAppAccess(atApp(owner, repo, app)));
}

export async function saveAppAccess(owner: string, repo: string, app: string, mode: AppAccessMode): Promise<AppAccess> {
  return unwrap(await sdkSetAppAccess({ ...atApp(owner, repo, app), body: { mode } }));
}
