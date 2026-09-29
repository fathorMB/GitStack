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

// Stesso aggancio per il 403 `password_change_required`: finche' la password
// iniziale non e' cambiata il gateway rifiuta ogni chiamata autenticata con
// quel code. Altri 403 (forbidden, insufficient_scope...) non fanno niente.
export function installPasswordChangeHandler(onRequired: () => void): () => void {
  const id = client.interceptors.response.use(async (response) => {
    if (response.status === 403) {
      try {
        const body = (await response.clone().json()) as { error?: { code?: string } };
        if (body?.error?.code === 'password_change_required') onRequired();
      } catch {
        // corpo non JSON: non e' il segnale.
      }
    }
    return response;
  });
  return () => client.interceptors.response.eject(id);
}
