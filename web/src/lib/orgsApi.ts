// Organizzazioni, membri e team via client generato (client di default:
// niente `client:` nelle options, cosi' l'interceptor 401 vede le risposte).
import {
  createOrganization,
  createTeam,
  deleteTeam,
  getOrganization,
  listOrganizations,
  listOrgMembers,
  listTeamMembers,
  listTeams,
  removeOrgMember,
  removeTeamMember,
  setOrgMember,
  setTeamMember,
} from '@gitstack/api-client';
import type {
  CreateOrganizationInput,
  CreateTeamInput,
  Organization,
  OrgMember,
  OrgRole,
  Team,
  TeamMember,
  TeamRole,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { Organization, OrgMember, OrgRole, Team, TeamMember, TeamRole };

const PER_PAGE = 100;

export async function fetchOrganizations(): Promise<Organization[]> {
  return unwrap(await listOrganizations({ baseUrl: API_BASE_URL, query: { perPage: PER_PAGE } })).items;
}

export async function createOrg(input: CreateOrganizationInput): Promise<Organization> {
  return unwrap(await createOrganization({ baseUrl: API_BASE_URL, body: input }));
}

export async function fetchOrganization(org: string): Promise<Organization> {
  return unwrap(await getOrganization({ baseUrl: API_BASE_URL, path: { org } }));
}

export async function fetchOrgMembers(org: string): Promise<OrgMember[]> {
  return unwrap(await listOrgMembers({ baseUrl: API_BASE_URL, path: { org }, query: { perPage: PER_PAGE } })).items;
}

export async function saveOrgMember(org: string, username: string, role: OrgRole): Promise<OrgMember> {
  return unwrap(await setOrgMember({ baseUrl: API_BASE_URL, path: { org, username }, body: { role } }));
}

export async function deleteOrgMember(org: string, username: string): Promise<void> {
  unwrapEmpty(await removeOrgMember({ baseUrl: API_BASE_URL, path: { org, username } }));
}

export async function fetchTeams(org: string): Promise<Team[]> {
  return unwrap(await listTeams({ baseUrl: API_BASE_URL, path: { org }, query: { perPage: PER_PAGE } })).items;
}

export async function createOrgTeam(org: string, input: CreateTeamInput): Promise<Team> {
  return unwrap(await createTeam({ baseUrl: API_BASE_URL, path: { org }, body: input }));
}

export async function deleteOrgTeam(org: string, team: string): Promise<void> {
  unwrapEmpty(await deleteTeam({ baseUrl: API_BASE_URL, path: { org, team } }));
}

export async function fetchTeamMembers(org: string, team: string): Promise<TeamMember[]> {
  return unwrap(await listTeamMembers({ baseUrl: API_BASE_URL, path: { org, team }, query: { perPage: PER_PAGE } })).items;
}

export async function saveTeamMember(org: string, team: string, username: string, role: TeamRole): Promise<TeamMember> {
  return unwrap(await setTeamMember({ baseUrl: API_BASE_URL, path: { org, team, username }, body: { role } }));
}

export async function deleteTeamMember(org: string, team: string, username: string): Promise<void> {
  unwrapEmpty(await removeTeamMember({ baseUrl: API_BASE_URL, path: { org, team, username } }));
}
