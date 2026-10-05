// Date leggibili per le liste (token, chiavi SSH).
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

export function usedText(lastUsedAt: string | null | undefined): string {
  return lastUsedAt ? `Last used ${formatDate(lastUsedAt)}` : 'Never used';
}

export function daysFromNow(days: number, now: Date = new Date()): string {
  return new Date(now.getTime() + days * 24 * 60 * 60 * 1000).toISOString();
}

const DAY_MS = 24 * 60 * 60 * 1000;

// Giorni interi rimasti prima della cancellazione definitiva (R2), minimo 0.
export function daysLeft(purgeAt: string, now: number = Date.now()): number {
  return Math.max(0, Math.ceil((Date.parse(purgeAt) - now) / DAY_MS));
}

// «3 days ago» per le liste del browser del codice.
export function timeAgo(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const s = Math.max(0, Math.round((now - t) / 1000));
  const units: [number, string][] = [
    [31536000, 'year'],
    [2592000, 'month'],
    [604800, 'week'],
    [86400, 'day'],
    [3600, 'hour'],
    [60, 'minute'],
  ];
  for (const [secs, name] of units) {
    if (s >= secs) {
      const n = Math.floor(s / secs);
      return `${n} ${name}${n === 1 ? '' : 's'} ago`;
    }
  }
  return 'just now';
}
