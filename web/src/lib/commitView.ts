import type { CommitSummary } from './codeApi';

/** Giorno (UTC) di un commit, per il raggruppamento: «Sep 27, 2026». */
export function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('en-US', { timeZone: 'UTC', month: 'short', day: 'numeric', year: 'numeric' });
}

export interface DayGroup {
  day: string;
  commits: CommitSummary[];
}

/** Raggruppa mantenendo l'ordine dell'API (dal piu' recente): un gruppo per ogni giorno consecutivo. */
export function groupByDay(commits: readonly CommitSummary[]): DayGroup[] {
  const groups: DayGroup[] = [];
  for (const c of commits) {
    const day = dayLabel(c.committer.date);
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.commits.push(c);
    else groups.push({ day, commits: [c] });
  }
  return groups;
}

/** Messaggio senza la prima riga (l'oggetto): il corpo del commit. */
export function messageBody(c: CommitSummary): string {
  const msg = c.message ?? '';
  const nl = msg.indexOf('\n');
  return nl < 0 ? '' : msg.slice(nl + 1).trim();
}
