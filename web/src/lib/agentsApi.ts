// Console admin degli agent (mockup 17) via client generato (client di
// default: niente `client:` nelle options, cosi' l'interceptor 401 vale).
import { createUser, createUserToken, deleteUser, listUsers, listUserTokens, revokeUserToken, updateUser } from '@gitstack/api-client';
import type { CreatedToken, CreateTokenInput, Token, User } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';
import { fetchOrganizations, fetchTeamMembers, fetchTeams } from './orgsApi';

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

export interface AgentTeam {
  org: string;
  team: string;
}

// Team di cui l'agent fa parte, ricavati da organizzazioni -> team -> membri
// (non c'e' un endpoint "team di un utente"). Grant diretti e visibilita'
// per repository non hanno ancora un'API per utente: vedi AgentsPage.
export async function fetchAgentTeams(username: string): Promise<AgentTeam[]> {
  const result: AgentTeam[] = [];
  for (const org of await fetchOrganizations()) {
    for (const team of await fetchTeams(org.name)) {
      const members = await fetchTeamMembers(org.name, team.name);
      if (members.some((m) => m.user.username === username)) result.push({ org: org.name, team: team.name });
    }
  }
  return result;
}
