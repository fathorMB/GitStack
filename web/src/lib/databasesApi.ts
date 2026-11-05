import {
  listDatabases, createDatabase, getDatabase, deleteDatabase,
  listDatabaseLinks, createDatabaseLink, deleteDatabaseLink, rotateDatabaseLink,
  listDatabaseBackups, createDatabaseBackup, restoreDatabaseBackup,
  getDatabaseLan, setDatabaseLan, rotateDatabaseCredentials,
} from '@gitstack/api-client';
import type { DatabaseCreateInput, DatabaseLinkCreateInput, DatabaseRestoreInput } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { Database, DatabaseCredentials, DatabaseLink, DatabaseBackup, DatabaseLan } from '@gitstack/api-client';

const at = (owner: string, name: string) => ({ baseUrl: API_BASE_URL, path: { owner, name } });

export const fetchDatabases = async (owner: string) => unwrap(await listDatabases({ baseUrl: API_BASE_URL, path: { owner } }));
export const addDatabase = async (owner: string, body: DatabaseCreateInput) => unwrap(await createDatabase({ baseUrl: API_BASE_URL, path: { owner }, body }));
export const fetchDatabase = async (owner: string, name: string) => unwrap(await getDatabase(at(owner, name)));
export const removeDatabase = async (owner: string, name: string, confirm: string) => unwrapEmpty(await deleteDatabase({ ...at(owner, name), query: { confirm } }));
export const fetchDatabaseLinks = async (owner: string, name: string) => unwrap(await listDatabaseLinks(at(owner, name)));
export const addDatabaseLink = async (owner: string, name: string, body: DatabaseLinkCreateInput) => unwrap(await createDatabaseLink({ ...at(owner, name), body }));
export const removeDatabaseLink = async (owner: string, name: string, linkId: string) => unwrap(await deleteDatabaseLink({ baseUrl: API_BASE_URL, path: { owner, name, linkId } }));
export const rotateLink = async (owner: string, name: string, linkId: string) => unwrap(await rotateDatabaseLink({ baseUrl: API_BASE_URL, path: { owner, name, linkId } }));
export const fetchDatabaseBackups = async (owner: string, name: string) => unwrap(await listDatabaseBackups(at(owner, name)));
export const addDatabaseBackup = async (owner: string, name: string) => unwrap(await createDatabaseBackup(at(owner, name)));
export const restoreBackup = async (owner: string, name: string, backupId: string, body: DatabaseRestoreInput) => unwrap(await restoreDatabaseBackup({ baseUrl: API_BASE_URL, path: { owner, name, backupId }, body }));
export const fetchDatabaseLan = async (owner: string, name: string) => unwrap(await getDatabaseLan(at(owner, name)));
export const changeDatabaseLan = async (owner: string, name: string, enabled: boolean) => unwrap(await setDatabaseLan({ ...at(owner, name), body: { enabled } }));
export const rotateCredentials = async (owner: string, name: string) => unwrap(await rotateDatabaseCredentials(at(owner, name)));
