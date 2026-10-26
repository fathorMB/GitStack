// Supporto alla pagina Volumes & resources (H3, H11, GIT-359): lettura dei campi di testo e avviso database.

/** vCPU come testo libero («0.5», «0,5 vCPU», «500m») in millesimi; null se non valido. */
export function parseCpuMillis(text: string): number | null {
  const m = /^(\d+(?:\.\d+)?)\s*(m|vcpu|cpu)?$/.exec(text.trim().toLowerCase().replace(',', '.'));
  if (!m) return null;
  const n = Number(m[1]);
  const millis = m[2] === 'm' ? n : n * 1000;
  return millis > 0 ? Math.round(millis) : null;
}

/** Memoria o disco come testo («768 MB», «1.5 GB», «512»), base 1024; senza unità vale `defaultUnit`. */
export function parseBytes(text: string, defaultUnit: 'MB' | 'GB' = 'MB'): number | null {
  const m = /^(\d+(?:\.\d+)?)\s*(kb|mb|gb|tb|kib|mib|gib|k|m|g)?$/.exec(text.trim().toLowerCase().replace(',', '.'));
  if (!m) return null;
  const unit = (m[2] ?? defaultUnit.toLowerCase()).charAt(0);
  const mult = unit === 'k' ? 1024 : unit === 'm' ? 1024 ** 2 : unit === 'g' ? 1024 ** 3 : 1024 ** 4;
  const n = Math.round(Number(m[1]) * mult);
  return n > 0 ? n : null;
}

/** Dimensione di un volume, come nel mockup («10 GB», «512 MB»). */
export function formatDisk(bytes: number): string {
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${Math.round(gb * 10) / 10} GB`;
  return `${Math.round(bytes / 1024 ** 2)} MB`;
}

const DB_HINT = /postgres|pgdata|mysql|mariadb|mongo|redis|couchdb|influx|cassandra|clickhouse|sqlite|\.db\b/i;

/** Il volume sembra contenere un database: lo dicono il percorso di mount o l'immagine (avviso del mockup). */
export function looksLikeDatabase(mountPath: string, imageReference?: string | null): boolean {
  return DB_HINT.test(mountPath) || (imageReference ? DB_HINT.test(imageReference) : false);
}
