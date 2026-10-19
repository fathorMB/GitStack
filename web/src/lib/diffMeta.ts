// Etichette e miniature comuni alle viste del diff (commit e Pull Request).
import type { FileDiff } from '@gitstack/api-client';

export const REASON_LABEL: Record<NonNullable<FileDiff['collapseReason']>, { badge: string; text: string }> = {
  large: { badge: 'Large diff', text: 'Large diffs (over 500 changed lines) are collapsed by default.' },
  lock: { badge: 'Lock file', text: 'Lock and generated files are collapsed by default.' },
  generated: { badge: 'Generated', text: 'Lock and generated files are collapsed by default.' },
};

export const STATUS_LABEL: Record<FileDiff['status'], string> = {
  added: 'Added',
  modified: 'Modified',
  deleted: 'Deleted',
  renamed: 'Renamed',
  copied: 'Copied',
};

/** Cinque quadratini proporzionali alle righe aggiunte e tolte, come nel mockup 10. */
export function diffstat(additions: number, deletions: number): ('a' | 'd' | '')[] {
  const total = additions + deletions;
  if (total === 0) return ['', '', '', '', ''];
  let a = Math.round((additions / total) * 5);
  if (additions > 0 && a === 0) a = 1;
  if (deletions > 0 && a === 5) a = 4;
  const d = Math.min(5 - a, deletions > 0 ? Math.max(1, 5 - a) : 0);
  return Array.from({ length: 5 }, (_, i) => (i < a ? 'a' : i < a + d ? 'd' : ''));
}
