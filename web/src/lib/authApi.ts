// Auth e profilo via client generato. Usa il client di default dell'SDK
// (niente `client:` nelle options), cosi' l'interceptor 401 di
// unauthorized.ts vede tutte le risposte.
import { getCurrentSession, listOidcProviders, login, logout, updateUser } from '@gitstack/api-client';
import type { CurrentSession, OidcProvider, UpdateUserInput, User } from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CurrentSession, OidcProvider, User };

export async function loginWithPassword(username: string, password: string): Promise<CurrentSession> {
  return unwrap(await login({ baseUrl: API_BASE_URL, body: { username, password } }));
}

export async function fetchSession(): Promise<CurrentSession> {
  return unwrap(await getCurrentSession({ baseUrl: API_BASE_URL }));
}

export async function signOut(): Promise<void> {
  unwrapEmpty(await logout({ baseUrl: API_BASE_URL }));
}

// Provider OIDC configurati. Con 501 (non ancora implementato), errore di
// rete o lista vuota risponde [] in silenzio: il login mostra solo la password.
export async function fetchOidcProviders(): Promise<OidcProvider[]> {
  try {
    return unwrap(await listOidcProviders({ baseUrl: API_BASE_URL })).items;
  } catch {
    return [];
  }
}

// URL di avvio del login OIDC: e' una navigazione del browser (redirect al
// provider), non una chiamata fetch.
export function oidcStartUrl(slug: string, redirectTo: string): string {
  return `${API_BASE_URL}/auth/oidc/${encodeURIComponent(slug)}/start?redirectTo=${encodeURIComponent(redirectTo)}`;
}

export async function updateProfile(username: string, input: UpdateUserInput): Promise<User> {
  return unwrap(await updateUser({ baseUrl: API_BASE_URL, path: { username }, body: input }));
}
