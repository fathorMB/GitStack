import { Check, X } from 'lucide-react';
import { runState } from '../../lib/actionsView';

/** Pallino di stato del mockup (.st): colore + icona; il testo lo dà il chiamante o l'aria-label. */
export function RunDot({ status, conclusion }: { status: string; conclusion?: string | null }) {
  const s = runState(status, conclusion);
  return (
    <span className={`st ${s.tone}`} role="img" aria-label={s.label}>
      {s.tone === 'ok' ? <Check size={12} aria-hidden="true" /> : null}
      {s.tone === 'err' || s.tone === 'skip' ? <X size={12} aria-hidden="true" /> : null}
    </span>
  );
}

const BADGE: Record<string, string> = { ok: 'badge badge-ok', err: 'badge badge-err', run: 'badge badge-run', wait: 'badge', skip: 'badge' };

export function RunBadge({ status, conclusion, duration }: { status: string; conclusion?: string | null; duration?: string }) {
  const s = runState(status, conclusion);
  return <span className={BADGE[s.tone]}>{duration ? `${s.label} · ${duration}` : s.label}</span>;
}
