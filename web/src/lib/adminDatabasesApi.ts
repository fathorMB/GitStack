// Console admin Database settings (GIT-557, ADR 0003, DB23) via il client generato.
import {
  getAdminDatabasesSettings as sdkSettings,
  listAdminAppOwnerQuotas as sdkQuotas,
  listAdminDatabases as sdkDatabases,
  putAdminAppOwnerQuota as sdkPutQuota,
  deleteAdminAppOwnerQuota as sdkDeleteQuota,
  updateAdminDatabasesSettings as sdkUpdateSettings,
} from '@gitstack/api-client';
import type { AppOwnerQuota, AppOwnerQuotaInput, Database, DatabasesInstanceSettings, DatabasesInstanceSettingsInput } from '@gitstack/api-client';
import { API_BASE_URL, ApiError, describeError, unwrap, unwrapEmpty } from './http';

const at = { baseUrl: API_BASE_URL };
export type { AppOwnerQuota, AppOwnerQuotaInput, Database, DatabasesInstanceSettings, DatabasesInstanceSettingsInput };

export type AdminDatabasesSection = 'settings' | 'databases' | 'quotas';
export interface AdminDatabasesData {
  /** Vero se la funzione è spenta nell'installazione (422 databases_disabled, DB23): niente 501. */
  disabled: boolean;
  settings: DatabasesInstanceSettings | null;
  databases: Database[] | null;
  /** La quota per owner è la stessa delle app (DB3). */
  quotas: AppOwnerQuota[] | null;
  errors: Partial<Record<AdminDatabasesSection, string>>;
}

// Ogni sezione si carica per conto suo: se una chiamata fallisce il suo valore è null e il messaggio sta in
// errors, le altre restano leggibili (lezione di GIT-547). Con la funzione spenta la console lo dice.
export async function fetchAdminDatabasesData(): Promise<AdminDatabasesData> {
  const errors: AdminDatabasesData['errors'] = {};
  let disabled = false;
  async function section<T>(name: AdminDatabasesSection, load: () => Promise<T>): Promise<T | null> {
    try {
      return await load();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'databases_disabled') disabled = true;
      else errors[name] = describeError(e);
      return null;
    }
  }
  const [settings, databases, quotas] = await Promise.all([
    section('settings', async () => unwrap(await sdkSettings(at))),
    section('databases', async () => unwrap(await sdkDatabases(at)).items),
    section('quotas', async () => unwrap(await sdkQuotas(at)).items),
  ]);
  return { disabled, settings, databases, quotas, errors };
}

export async function saveDatabasesSettings(settings: DatabasesInstanceSettingsInput): Promise<DatabasesInstanceSettings> {
  return unwrap(await sdkUpdateSettings({ ...at, body: settings }));
}
export async function saveDatabaseOwnerQuota(owner: string, limits: AppOwnerQuotaInput): Promise<AppOwnerQuota> {
  return unwrap(await sdkPutQuota({ ...at, path: { owner }, body: limits }));
}
export async function resetDatabaseOwnerQuota(owner: string): Promise<AppOwnerQuota[]> {
  unwrapEmpty(await sdkDeleteQuota({ ...at, path: { owner } }));
  return unwrap(await sdkQuotas(at)).items;
}
