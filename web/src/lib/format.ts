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
