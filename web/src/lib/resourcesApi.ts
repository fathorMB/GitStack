// Punto della SPA (con authApi.ts e accessApi.ts) che importa il client TS
// generato (T-03, client/ts/src/generated/**, non modificato a mano qui: si
// usano solo le funzioni esportate da '@gitstack/api-client'). Nessuna
// chiamata diretta a un servizio diverso dal gateway (D7, API-first).
import { createResource, listResources } from '@gitstack/api-client';
import type { CreateResourceInput, Resource, ResourceList } from '@gitstack/api-client';
import { API_BASE_URL, ApiError, unwrap } from './http';

export { API_BASE_URL, ApiError };

export async function fetchResources(): Promise<ResourceList> {
  return unwrap(await listResources({ baseUrl: API_BASE_URL }));
}

export async function createTestResource(input: CreateResourceInput): Promise<Resource> {
  return unwrap(await createResource({ baseUrl: API_BASE_URL, body: input }));
}
