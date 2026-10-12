// Admin · System (mockup 16) via client generato (client di default: niente
// `client:` nelle options, cosi' l'interceptor 401 vale).
import { getAdminSystem } from '@gitstack/api-client';
import type { AdminSystem } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { AdminSystem };

export async function fetchSystem(): Promise<AdminSystem> {
  return unwrap(await getAdminSystem({ baseUrl: API_BASE_URL }));
}
