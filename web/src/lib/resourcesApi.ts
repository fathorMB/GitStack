// Unico punto della SPA che importa il client TS generato (T-03,
// client/ts/src/generated/**, non modificato a mano qui: si usano solo le
// funzioni esportate da '@gitstack/api-client'). Nessuna chiamata diretta a
// un servizio diverso dal gateway (D7, API-first): API_BASE_URL è relativa,
// mai un host assoluto di identity/git/core/eventbus.
import { createResource, listResources } from '@gitstack/api-client';
import type { CreateResourceInput, Error as ApiErrorBody, Resource, ResourceList } from '@gitstack/api-client';

// Relativa apposta: in produzione il container web/ (vedi web/Dockerfile)
// inoltra /api/* al gateway; in sviluppo lo fa il proxy di Vite (vedi
// vite.config.ts). La SPA non conosce host o porta del gateway.
export const API_BASE_URL = '/api';

export class ApiError extends Error {
  readonly code: string;

  constructor(body: ApiErrorBody) {
    super(body.error.message);
    this.name = 'ApiError';
    this.code = body.error.code;
  }
}

function unwrapEmptyBody(): never {
  throw new Error('Risposta inattesa dal gateway: corpo vuoto.');
}

export async function fetchResources(): Promise<ResourceList> {
  const { data, error } = await listResources({ baseUrl: API_BASE_URL });
  if (error) {
    throw new ApiError(error);
  }
  return data ?? unwrapEmptyBody();
}

export async function createTestResource(input: CreateResourceInput): Promise<Resource> {
  const { data, error } = await createResource({ baseUrl: API_BASE_URL, body: input });
  if (error) {
    throw new ApiError(error);
  }
  return data ?? unwrapEmptyBody();
}
