// Console admin Apps & resources (mockup H9) via il client generato.
import {
  addAdminAppTrustedKey as sdkAddKey,
  deleteAdminAppOwnerQuota as sdkDeleteQuota,
  deleteAdminAppTrustedKey as sdkDeleteKey,
  getAdminAppsCapacity as sdkCapacity,
  getAdminAppsEgress as sdkEgress,
  updateAdminAppsEgress as sdkUpdateEgress,
  getAdminAppsSettings as sdkSettings,
  getAppAccess as sdkGetAppAccess,
  listAppDomains as sdkListAppDomains,
  listAdminAppOwnerQuotas as sdkQuotas,
  listAdminAppTrustedKeys as sdkKeys,
  listAdminApps as sdkApps,
  putAdminAppOwnerQuota as sdkPutQuota,
  updateAdminAppsReserve as sdkReserve,
  updateAdminAppsSettings as sdkUpdateSettings,
} from '@gitstack/api-client';
import type { AdminApp, AppDomain, AppsLetsEncryptStatus, AppOwnerQuota, AppOwnerQuotaInput, AppResourceAmount, AppTrustedKey, AppsCapacity, AppsEgressSettings, AppPrivateDestination, AppPrivateDestinationInput, AppsInstanceSettings, AppsInstanceSettingsInput } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

const at = { baseUrl: API_BASE_URL };
export type { AdminApp, AppDomain, AppsLetsEncryptStatus, AppOwnerQuota, AppOwnerQuotaInput, AppResourceAmount, AppTrustedKey, AppsCapacity, AppsEgressSettings, AppPrivateDestination, AppPrivateDestinationInput, AppsInstanceSettings, AppsInstanceSettingsInput };

export async function fetchAdminAppsData() {
  const [capacity, quotas, settings, keys, apps, egress] = await Promise.all([
    sdkCapacity(at), sdkQuotas(at), sdkSettings(at), sdkKeys(at), sdkApps(at), sdkEgress(at),
  ]);
  return { capacity: unwrap(capacity), quotas: unwrap(quotas).items, settings: unwrap(settings), keys: unwrap(keys).items, apps: unwrap(apps).items, egress: unwrap(egress) };
}

export async function updateAppsReserve(share: AppResourceAmount): Promise<AppsCapacity> {
  return unwrap(await sdkReserve({ ...at, body: share }));
}
export async function saveOwnerQuota(owner: string, limits: AppOwnerQuotaInput): Promise<AppOwnerQuota> {
  return unwrap(await sdkPutQuota({ ...at, path: { owner }, body: limits }));
}
export async function resetOwnerQuota(owner: string): Promise<void> {
  unwrapEmpty(await sdkDeleteQuota({ ...at, path: { owner } }));
}
export async function fetchOwnerQuotas(): Promise<AppOwnerQuota[]> {
  return unwrap(await sdkQuotas(at)).items;
}
export async function saveAppsSettings(settings: AppsInstanceSettingsInput): Promise<AppsInstanceSettings> {
  return unwrap(await sdkUpdateSettings({ ...at, body: settings }));
}
export async function addTrustedKey(name: string, publicKey: string): Promise<AppTrustedKey> {
  return unwrap(await sdkAddKey({ ...at, body: { name, publicKey } }));
}
export async function removeTrustedKey(id: string): Promise<void> {
  unwrapEmpty(await sdkDeleteKey({ ...at, path: { keyId: id } }));
}

// Destinazioni private aperte all'uscita delle app (H16): l'elenco si sostituisce per intero.
export async function saveAppsEgress(privateDestinations: AppPrivateDestinationInput[]): Promise<AppsEgressSettings> {
  return unwrap(await sdkUpdateEgress({ ...at, body: { privateDestinations } }));
}

// Elenco delle app pubbliche dell'istanza (v3-c): AdminApp non porta accesso né domini,
// quindi per ogni app si legge l'accesso e, se è pubblica, i domini con lo stato del certificato.
export type PublicApp = { owner: string; repo: string; name: string; address: string | null; domains: AppDomain[] };

export async function fetchPublicApps(apps: { owner: string; repo: string; name: string }[]): Promise<PublicApp[]> {
  const found = await Promise.all(apps.map(async (app) => {
    const path = { owner: app.owner, repo: app.repo, app: app.name };
    const access = unwrap(await sdkGetAppAccess({ ...at, path }));
    if (access.mode !== 'public') return null;
    const domains = unwrap(await sdkListAppDomains({ ...at, path })).items;
    return { owner: app.owner, repo: app.repo, name: app.name, address: access.address ?? null, domains };
  }));
  return found.filter((app): app is PublicApp => app !== null);
}
