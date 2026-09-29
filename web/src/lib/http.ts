// Parti comuni dei wrapper del client generato (lib/*Api.ts): base URL,
// errore tipizzato e trasformazione del risultato { data, error } dell'SDK.
// Nessun file fuori da lib/ importa '@gitstack/api-client'.
import type { Error as ApiErrorBody } from '@gitstack/api-client';

// Relativa apposta: in produzione il container web/ (vedi web/Dockerfile)
// inoltra /api/* al gateway; in sviluppo lo fa il proxy di Vite (vite.config.ts).
export const API_BASE_URL = '/api';

export class ApiError extends Error {
  readonly code: string;
  readonly status?: number;
  /** Secondi da attendere (header Retry-After, 429). */
  readonly retryAfter?: number;
  readonly details?: Record<string, unknown>;

  constructor(body: ApiErrorBody, status?: number, retryAfter?: number) {
    super(body.error.message);
    this.name = 'ApiError';
    this.code = body.error.code;
    this.status = status;
    this.retryAfter = retryAfter;
    this.details = body.error.details;
  }
}

interface SdkResult<T> {
  data?: T;
  error?: unknown;
  response?: Response;
}

function isApiErrorBody(v: unknown): v is ApiErrorBody {
  return typeof v === 'object' && v !== null && 'error' in v && typeof (v as ApiErrorBody).error?.code === 'string';
}

/** Restituisce `data` o lancia ApiError (mai un errore muto). */
export function unwrap<T>(result: SdkResult<T>): T {
  const { data, error, response } = result;
  if (error !== undefined && error !== null && error !== '') {
    const status = response?.status;
    const ra = Number(response?.headers.get('Retry-After'));
    const body: ApiErrorBody = isApiErrorBody(error)
      ? error
      : { error: { code: 'unexpected', message: `Unexpected response from the server${status ? ` (${status})` : ''}.` } };
    throw new ApiError(body, status, Number.isFinite(ra) && ra > 0 ? ra : undefined);
  }
  if (data === undefined) {
    throw new Error('Unexpected empty response from the gateway.');
  }
  return data;
}

/** Come unwrap, per le risposte senza corpo (204). */
export function unwrapEmpty(result: SdkResult<unknown>): void {
  const { error, response } = result;
  if (error !== undefined && error !== null && error !== '') {
    unwrap({ error, response });
  }
}

function waitText(seconds: number): string {
  if (seconds < 90) return `${seconds} second${seconds === 1 ? '' : 's'}`;
  const minutes = Math.ceil(seconds / 60);
  return `${minutes} minutes`;
}

/** Messaggio leggibile per la UI (401, 403, 429, 422, rete). */
export function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 401:
        return err.code === 'invalid_credentials'
          ? 'Wrong username or password.'
          : 'Your session has expired. Please sign in again.';
      case 403:
        return `You don't have permission to do this. ${err.message}`.trim();
      case 429:
        return err.retryAfter
          ? `Too many attempts. Try again in ${waitText(err.retryAfter)}.`
          : 'Too many attempts. Try again later.';
      case 422: {
        const fields = err.details?.fields;
        if (fields && typeof fields === 'object') {
          const parts = Object.entries(fields as Record<string, unknown>).map(([k, v]) => `${k}: ${String(v)}`);
          if (parts.length > 0) return `${err.message} (${parts.join('; ')})`;
        }
        return err.message;
      }
      default:
        return err.message;
    }
  }
  if (err instanceof Error && err.name === 'ApiError') return err.message;
  return 'Could not reach the server. Check your connection and try again.';
}

/** Errori per campo di un 422 validation_failed (details.fields), altrimenti {}. */
export function fieldErrors(err: unknown): Record<string, string> {
  if (!(err instanceof ApiError) || err.status !== 422) return {};
  const fields = err.details?.fields;
  if (!fields || typeof fields !== 'object') return {};
  return Object.fromEntries(Object.entries(fields as Record<string, unknown>).map(([k, v]) => [k, String(v)]));
}
