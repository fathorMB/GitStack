// Token personali e chiavi SSH via client generato (client di default).
import {
  addSshKey,
  createToken,
  deleteSshKey,
  listOwnSessions,
  listSshKeys,
  listTokens,
  revokeOwnSession,
  revokeToken,
} from '@gitstack/api-client';
import type {
  AddSshKeyInput,
  CreatedToken,
  CreateTokenInput,
  Session,
  SshKey,
  Token,
  TokenScope,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CreatedToken, Session, SshKey, Token, TokenScope };

// Catalogo scope del contratto (schema TokenScope): non allargare senza il CTO.
export const TOKEN_SCOPES: { value: TokenScope; description: string }[] = [
  { value: 'read:user', description: 'Read your profile' },
  { value: 'write:user', description: 'Update your profile' },
  { value: 'read:org', description: 'Read organizations, teams and members' },
  { value: 'write:org', description: 'Change teams and memberships' },
  { value: 'admin:org', description: 'Manage members, teams and permissions' },
  { value: 'read:resource', description: 'Read resources' },
  { value: 'write:resource', description: 'Create, change and delete resources' },
];

export async function fetchTokens(): Promise<Token[]> {
  return unwrap(await listTokens({ baseUrl: API_BASE_URL })).items;
}

export async function createPersonalToken(input: CreateTokenInput): Promise<CreatedToken> {
  return unwrap(await createToken({ baseUrl: API_BASE_URL, body: input }));
}

export async function revokePersonalToken(tokenId: string): Promise<void> {
  unwrapEmpty(await revokeToken({ baseUrl: API_BASE_URL, path: { tokenId } }));
}

// Sessioni web attive dell'utente (F12).
export async function fetchSessions(): Promise<Session[]> {
  return unwrap(await listOwnSessions({ baseUrl: API_BASE_URL })).items;
}

export async function endSession(sessionId: string): Promise<void> {
  unwrapEmpty(await revokeOwnSession({ baseUrl: API_BASE_URL, path: { sessionId } }));
}

export async function fetchSshKeys(): Promise<SshKey[]> {
  return unwrap(await listSshKeys({ baseUrl: API_BASE_URL })).items;
}

export async function createSshKey(input: AddSshKeyInput): Promise<SshKey> {
  return unwrap(await addSshKey({ baseUrl: API_BASE_URL, body: input }));
}

export async function removeSshKey(keyId: string): Promise<void> {
  unwrapEmpty(await deleteSshKey({ baseUrl: API_BASE_URL, path: { keyId } }));
}
