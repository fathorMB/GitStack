import type { PullMirror } from '@gitstack/api-client';

// Stato del mirror estratto da PullMirror per il banner e il badge.
export type MirrorVariant = 'active' | 'paused' | 'diverged' | 'stale';

/** Determina la variante visiva dato un PullMirror o undefined. */
export function mirrorVariant(pm: PullMirror | null | undefined): MirrorVariant {
  if (!pm) return 'active';
  if (pm.state === 'paused') return 'paused';
  if (pm.state === 'stopped') return 'stale';
  if (pm.divergedRefs.length > 0) return 'diverged';

  // Se lastSyncedAt e' null o oltre 24h: stale.
  if (!pm.lastSyncedAt) return 'stale';
  const elapsed = Date.now() - new Date(pm.lastSyncedAt).getTime();
  if (elapsed > 24 * 60 * 60 * 1000) return 'stale';
  return 'active';
}
