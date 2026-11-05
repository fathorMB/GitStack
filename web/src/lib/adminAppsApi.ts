// Console admin Apps & resources (mockup H9) via il client generato.
import {
  addAdminAppTrustedKey as sdkAddKey,
  deleteAdminAppOwnerQuota as sdkDeleteQuota,
  deleteAdminAppTrustedKey as sdkDeleteKey,
  getAdminAppsCapacity as sdkCapacity,
  getAdminAppsEgress as sdkEgress,
  updateAdminAppsEgress as sdkUpdateEgress,
  getAdminAppsSettings as sdkSettings,
  listAdminPublicApps as sdkPublicApps,
  listAdminAppOwnerQuotas as sdkQuotas,
  listAdminAppTrustedKeys as sdkKeys,
  listAdminApps as sdkApps,
  putAdminAppOwnerQuota as sdkPutQuota,
  updateAdminAppsReserve as sdkReserve,
  updateAdminAppsSettings as sdkUpdateSettings,
} from '@gitstack/api-client';
import type { AdminApp, AdminPublicApp, AdminPublicAppDomain, AppsLetsEncryptStatus, AppOwnerQuota, AppOwnerQuotaInput, AppResourceAmount, AppTrustedKey, AppsCapacity, AppsEgressSettings, AppPrivateDestination, AppPrivateDestinationInput, AppsInstanceSettings, AppsInstanceSettingsInput } from '@gitstack/api-client';
import { API_BASE_URL, describeError, unwrap, unwrapEmpty } from './http';

const at = { baseUrl: API_BASE_URL };
export type { AdminApp, AdminPublicApp, AdminPublicAppDomain, AppsLetsEncryptStatus, AppOwnerQuota, AppOwnerQuotaInput, AppResourceAmount, AppTrustedKey, AppsCapacity, AppsEgressSettings, AppPrivateDestination, AppPrivateDestinationInput, AppsInstanceSettings, AppsInstanceSettingsInput };

export type AdminAppsSection = 'capacity' | 'quotas' | 'settings' | 'keys' | 'apps' | 'egress';
// Ogni sezione si carica per conto suo: se una chiamata fallisce il suo valore è null e il messaggio
// sta in errors, le altre restano leggibili (GIT-547).
export interface AdminAppsData {
  capacity: AppsCapacity | null;
  quotas: AppOwnerQuota[] | null;
  settings: AppsInstanceSettings | null;
  keys: AppTrustedKey[] | null;
  apps: AdminApp[] | null;
  egress: AppsEgressSettings | null;
  errors: Partial<Record<AdminAppsSection, string>>;
}

export async function fetchAdminAppsData(): Promise<AdminAppsData> {
  const errors: AdminAppsData['errors'] = {};
  async function section<T>(name: AdminAppsSection, load: () => Promise<T>): Promise<T | null> {
    try { return await load(); } catch (e) { errors[name] = describeError(e); return null; }
  }
  const [capacity, quotas, settings, keys, apps, egress] = await Promise.all([
    section('capacity', async () => unwrap(await sdkCapacity(at))),
    section('quotas', async () => unwrap(await sdkQuotas(at)).items),
    section('settings', async () => unwrap(await sdkSettings(at))),
    section('keys', async () => unwrap(await sdkKeys(at)).items),
    section('apps', async () => unwrap(await sdkApps(at)).items),
    section('egress', async () => unwrap(await sdkEgress(at))),
  ]);
  return { capacity, quotas, settings, keys, apps, egress, errors };
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

// Elenco delle app pubbliche dell'istanza (v3-c): una sola chiamata admin, con dominio attivo e
// stato del certificato per app (GIT-473), senza leggere accesso e domini app per app.
export type PublicApp = AdminPublicApp;

export async function fetchPublicApps(): Promise<PublicApp[]> {
  return unwrap(await sdkPublicApps(at)).items;
}
