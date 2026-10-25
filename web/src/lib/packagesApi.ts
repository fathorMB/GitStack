// Registry di immagini (RG1-RG2, mockup B5 e B6): operazioni generate di @gitstack/api-client.
// Usa il client di default (nessun client esplicito) per l'interceptor 401.
import {
  deleteRepositoryPackage as sdkDeletePackage,
  getRepositoryPackageSettings as sdkGetSettings,
  listRepositoryPackages as sdkListPackages,
  updateRepositoryPackageSettings as sdkUpdateSettings,
} from '@gitstack/api-client';
import type { Package, PackageList, PackageSettings, PackageSettingsInput } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { Package, PackageList, PackageSettings, PackageSettingsInput };

export async function fetchPackages(owner: string, repo: string): Promise<PackageList> {
  return unwrap(await sdkListPackages({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

/** Numero di immagini pubblicate nel repository. */
export async function countPackages(owner: string, repo: string): Promise<number> {
  return (await fetchPackages(owner, repo)).items.length;
}

/** Elimina l'immagine o, con `tag`, solo quel tag. */
export async function deletePackage(owner: string, repo: string, name: string, tag?: string): Promise<void> {
  unwrapEmpty(await sdkDeletePackage({ baseUrl: API_BASE_URL, path: { owner, repo }, query: tag ? { name, tag } : { name } }));
}

export async function fetchPackageSettings(owner: string, repo: string): Promise<PackageSettings> {
  return unwrap(await sdkGetSettings({ baseUrl: API_BASE_URL, path: { owner, repo } }));
}

export async function savePackageSettings(owner: string, repo: string, input: PackageSettingsInput): Promise<PackageSettings> {
  return unwrap(await sdkUpdateSettings({ baseUrl: API_BASE_URL, path: { owner, repo }, body: input }));
}

/** Comando di pull: l'host è quello da cui si serve l'interfaccia (il registry è alla radice dello stesso host). */
export function pullCommand(host: string, name: string, tag: string | undefined, digest: string | undefined): string {
  if (tag) return `docker pull ${host}/${name}:${tag}`;
  return digest ? `docker pull ${host}/${name}@${digest}` : `docker pull ${host}/${name}`;
}
