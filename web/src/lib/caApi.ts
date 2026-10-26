// Guida CA per i tester (H2, N5, GIT-383): impronta della CA interna da core
// (GET /instance/ca, pubblica) e prova di fiducia verso un'app.
// Client di default (nessun `client:` nelle options), come gli altri lib/*Api.ts.
import { getInstanceCa } from '@gitstack/api-client';
import type { InstanceCa } from '@gitstack/api-client';
import { API_BASE_URL, unwrap } from './http';

export type { InstanceCa };

export async function fetchInstanceCa(): Promise<InstanceCa> {
  return unwrap(await getInstanceCa({ baseUrl: API_BASE_URL }));
}

export type CaDevice = 'win' | 'mac' | 'ios' | 'and';

export const CA_DEVICES: { id: CaDevice; label: string }[] = [
  { id: 'win', label: 'Windows' },
  { id: 'mac', label: 'macOS' },
  { id: 'ios', label: 'iPhone / iPad' },
  { id: 'and', label: 'Android' },
];

/** Sistema dallo user agent. iPadOS 13+ si presenta come Macintosh: lo distingue il touch. Predefinito: Windows. */
export function detectDevice(userAgent: string, maxTouchPoints = 0): CaDevice {
  if (/iPhone|iPad|iPod/i.test(userAgent)) return 'ios';
  if (/Android/i.test(userAgent)) return 'and';
  if (/Macintosh|Mac OS X/i.test(userAgent)) return maxTouchPoints > 1 ? 'ios' : 'mac';
  return 'win';
}

// Solo un nome host (con porta facoltativa): lo schema e il percorso, se presenti, si scartano.
const HOST_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:\d{1,5})?$/i;

/** Indirizzi dei parametri `?app=` del link d'invito: host validi, senza doppioni. */
export function parseAppParams(search: string): string[] {
  const out: string[] = [];
  for (const raw of new URLSearchParams(search).getAll('app')) {
    const host = raw.trim().replace(/^https?:\/\//i, '').split('/')[0] ?? '';
    if (host && HOST_RE.test(host) && !out.includes(host)) out.push(host);
  }
  return out;
}

export type TrustResult = 'trusted' | 'untrusted';

/**
 * «Prova»: una fetch no-cors in HTTPS all'indirizzo dell'app. Con la CA fidata la
 * richiesta riesce (risposta opaca); con un certificato non fidato il browser
 * la rifiuta a livello TLS e la fetch lancia. Non distingue un errore TLS da un'app
 * spenta: in entrambi i casi la guida dice «non fidato» e invita a riprovare.
 */
export async function probeTrust(host: string, timeoutMs = 8000): Promise<TrustResult> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    await fetch(`https://${host}/`, { mode: 'no-cors', cache: 'no-store', signal: ctrl.signal });
    return 'trusted';
  } catch {
    return 'untrusted';
  } finally {
    clearTimeout(timer);
  }
}
