// Console admin degli agent (mockup 17) via client generato (client di
// default: niente `client:` nelle options, cosi' l'interceptor 401 vale).
import { createUser, createUserToken, deleteUser, getUserAccess, listUsers, listUserTokens, revokeUserToken, updateUser } from '@gitstack/api-client';
import type { AccessSource, CreatedToken, CreateTokenInput, ResourceRole, Token, User } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CreatedToken, Token, User };

const PER_PAGE = 100;

// Gli agent sono utenti con kind=agent: l'API non ha un filtro per kind,
// quindi si scorrono le pagine di /users e si tengono solo gli agent.
export async function fetchAgents(): Promise<User[]> {
  const agents: User[] = [];
  for (let page = 1; page <= 50; page++) {
    const res = unwrap(await listUsers({ baseUrl: API_BASE_URL, query: { page, perPage: PER_PAGE } }));
    agents.push(...res.items.filter((u) => u.kind === 'agent'));
    if (page * res.perPage >= res.total || res.items.length === 0) break;
  }
  return agents;
}

// Niente password: l'agent entra solo con i token creati dall'admin (P4).
export async function createAgent(username: string, displayName: string): Promise<User> {
  return unwrap(
    await createUser({
      baseUrl: API_BASE_URL,
      body: { username, kind: 'agent', ...(displayName ? { displayName } : {}) },
    }),
  );
}

export async function deleteAgent(username: string): Promise<void> {
  unwrapEmpty(await deleteUser({ baseUrl: API_BASE_URL, path: { username } }));
}

export async function setAgentActive(username: string, isActive: boolean): Promise<User> {
  return unwrap(await updateUser({ baseUrl: API_BASE_URL, path: { username }, body: { isActive } }));
}

export async function fetchAgentTokens(username: string): Promise<Token[]> {
  return unwrap(await listUserTokens({ baseUrl: API_BASE_URL, path: { username }, query: { perPage: PER_PAGE } })).items;
}

export async function createAgentToken(username: string, input: CreateTokenInput): Promise<CreatedToken> {
  return unwrap(await createUserToken({ baseUrl: API_BASE_URL, path: { username }, body: input }));
}

export async function revokeAgentToken(username: string, tokenId: string): Promise<void> {
  unwrapEmpty(await revokeUserToken({ baseUrl: API_BASE_URL, path: { username, tokenId } }));
}

export interface AgentAccess {
  fullName: string;
  role: ResourceRole;
  // Provenienza leggibile del ruolo, es. 'via team acme/agents, direct grant'.
  from: string;
}

function describeSource(s: AccessSource): string {
  switch (s.kind) {
    case 'direct':
      return 'direct grant';
    case 'team':
      return `via team ${s.organization ?? ''}/${s.team ?? ''}`;
    case 'owner':
      return s.organization ? `owner of ${s.organization}` : 'owner';
    case 'internal':
      return 'internal repository';
    case 'installation_admin':
      return 'installation admin';
  }
}

// Repository raggiungibili dall'agent con ruolo e provenienza (P1-P6),
// da GET /users/{username}/access (solo amministratori). Le fonti arrivano
// dalla piu' forte alla piu' debole.
export async function fetchAgentAccess(username: string): Promise<AgentAccess[]> {
  const result: AgentAccess[] = [];
  for (let page = 1; page <= 50; page++) {
    const res = unwrap(await getUserAccess({ baseUrl: API_BASE_URL, path: { username }, query: { page, perPage: PER_PAGE } }));
    for (const it of res.items) {
      result.push({ fullName: it.fullName, role: it.role, from: it.sources.map(describeSource).join(', ') });
    }
    if (page * res.perPage >= res.total || res.items.length === 0) break;
  }
  return result;
}
