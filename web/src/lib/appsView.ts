import type { App, AppAccessMode, AppResourceAmount, AppStatus } from '@gitstack/api-client';

/** Etichetta e tono del badge di stato (mockup H1/H3). */
export function statusBadge(status: AppStatus, runningPreviousVersion = false): { label: string; tone: 'ok' | 'run' | 'err' | 'none' } {
  if (runningPreviousVersion && status === 'running') return { label: 'Running previous version', tone: 'run' };
  switch (status) {
    case 'running':
      return { label: 'Running', tone: 'ok' };
    case 'starting':
      return { label: 'Starting', tone: 'run' };
    case 'restarting':
      return { label: 'Restarting', tone: 'err' };
    case 'unhealthy':
      return { label: 'Unhealthy', tone: 'err' };
    case 'failed':
      return { label: 'Failed', tone: 'err' };
    case 'stopped':
      return { label: 'Stopped', tone: 'none' };
  }
}

/** Etichetta del modo di accesso (H1, H6). */
export function accessLabel(mode: AppAccessMode): string {
  switch (mode) {
    case 'login':
      return 'Internal · GitStack login';
    case 'none':
      return 'Internal · no login';
    case 'public':
      return 'Public';
  }
}

/** vCPU con al massimo due decimali: 500 -> «0.5», 2100 -> «2.1». */
export function formatCpu(millis: number): string {
  return String(Math.round((millis / 1000) * 100) / 100);
}

/** Memoria in MB o GB (base 1024, come nel mockup: 512 MB, 1 GB). */
export function formatMemory(bytes: number): string {
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) return `${Math.round((mb / 1024) * 10) / 10} GB`;
  return `${Math.round(mb)} MB`;
}

export function formatAmount(a: AppResourceAmount): string {
  return `${formatCpu(a.cpuMillis)} vCPU · ${formatMemory(a.memoryBytes)}`;
}

/** Percentuale 0-100 di `value` su `of`, limitata. */
export function percent(value: number, of: number): number {
  if (of <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((value / of) * 100)));
}

/**
 * Indirizzo mostrato nell'elenco (v3-c): `publicAddress` lo calcola core (dominio proprio
 * verificato di un'app pubblica, altrimenti l'indirizzo automatico, H2) e lo manda a chiunque
 * legga l'app: non serve essere admin.
 */
export function listAddress(app: Pick<App, 'address' | 'publicAddress'>): string | null {
  return app.publicAddress ?? app.address ?? null;
}

/** L'app e' raggiungibile solo se ha un indirizzo ed e' in esecuzione. */
export function canOpen(app: Pick<App, 'address' | 'status'>): boolean {
  return Boolean(app.address) && app.status === 'running';
}

/** Digest corto stile «sha-7f3a91c» per le versioni. */
export function shortDigest(digest: string | null | undefined): string | null {
  if (!digest) return null;
  const hex = digest.replace(/^sha256:/, '');
  return `sha-${hex.slice(0, 7)}`;
}
