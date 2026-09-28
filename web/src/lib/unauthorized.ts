// Aggancio del 401 al client generato (@hey-api): un interceptor di risposta
// sul client di default (quello usato da tutte le funzioni dell'SDK, vedi
// resourcesApi.ts) avvisa la SPA quando il gateway risponde 401, cosi' il
// router puo' rimandare al login. Il client generato non si modifica.
import { client } from '@gitstack/api-client/src/generated/client.gen';

export function installUnauthorizedHandler(onUnauthorized: () => void): () => void {
  const id = client.interceptors.response.use((response) => {
    if (response.status === 401) {
      onUnauthorized();
    }
    return response;
  });
  return () => client.interceptors.response.eject(id);
}
