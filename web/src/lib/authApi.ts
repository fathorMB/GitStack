// Auth e profilo via client generato. Usa il client di default dell'SDK
// (niente `client:` nelle options), cosi' l'interceptor 401 di
// unauthorized.ts vede tutte le risposte.
import {
  changePassword,
  disableTotp,
  enrollTotp,
  getCurrentSession,
  getTotpStatus,
  listOidcProviders,
  login,
  logout,
  updateUser,
  verifyTotp,
} from '@gitstack/api-client';
import type {
  CurrentSession,
  OidcProvider,
  TotpEnrollment,
  TotpStatus,
  UpdateUserInput,
  User,
} from '@gitstack/api-client';
import { API_BASE_URL, unwrap, unwrapEmpty } from './http';

export type { CurrentSession, OidcProvider, TotpEnrollment, TotpStatus, User };

/** Secondo passo del login (F11): un codice TOTP oppure un codice di recupero. */
export interface SecondFactor {
  totpCode?: string;
  recoveryCode?: string;
}

/** Codice di errore del primo passo quando l'utente ha il TOTP attivo. */
export const SECOND_FACTOR_REQUIRED = 'second_factor_required';

export async function loginWithPassword(
  username: string,
  password: string,
  second?: SecondFactor,
): Promise<CurrentSession> {
  return unwrap(await login({ baseUrl: API_BASE_URL, body: { username, password, ...second } }));
}

// Secondo fattore TOTP (GET /auth/totp, POST /auth/totp/{enroll,verify,disable}).
export async function fetchTotpStatus(): Promise<TotpStatus> {
  return unwrap(await getTotpStatus({ baseUrl: API_BASE_URL }));
}

export async function startTotpEnrollment(): Promise<TotpEnrollment> {
  return unwrap(await enrollTotp({ baseUrl: API_BASE_URL }));
}

/** Conferma l'attivazione; ritorna i codici di recupero (visibili una sola volta). */
export async function confirmTotp(code: string): Promise<string[]> {
  return unwrap(await verifyTotp({ baseUrl: API_BASE_URL, body: { code } })).recoveryCodes;
}

export async function disableOwnTotp(password: string, second: SecondFactor): Promise<void> {
  const { totpCode, recoveryCode } = second;
  unwrapEmpty(
    await disableTotp({ baseUrl: API_BASE_URL, body: { password, code: totpCode, recoveryCode } }),
  );
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

// Cambio password (PUT /users/{username}/password, 204). Per la propria utenza
// serve currentPassword.
export async function changeOwnPassword(username: string, currentPassword: string, newPassword: string): Promise<void> {
  unwrapEmpty(
    await changePassword({ baseUrl: API_BASE_URL, path: { username }, body: { currentPassword, newPassword } }),
  );
}
