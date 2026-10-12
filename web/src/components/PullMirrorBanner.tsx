import { Clock, GitBranch, Info, ShieldAlert } from 'lucide-react';
import type { PullMirror } from '@gitstack/api-client';
import { timeAgo } from '../lib/format';
import { mirrorVariant } from '../lib/mirrorUtils';

// Tipo leggero per il mirror (origine + ultima sincronizzazione).
type RepositoryMirror = { origin?: string; lastSyncedAt?: string | null };

/** Badge compatto: "Mirror" con origine e ultima sincronizzazione. Prende il tipo lightweight del repo. */
export function PullMirrorBadge({ mirror }: { mirror: RepositoryMirror | null }) {
  if (!mirror || !mirror.origin) return null;
  return (
    <span className="row" style={{ gap: 6, alignItems: 'center' }}>
      <span className="badge badge-mirror" style={{ background: 'var(--accent-soft)', color: 'var(--accent-text)', fontWeight: 600 }}>
        <GitBranch size={14} aria-hidden="true" />
        Mirror
      </span>
      <span className="muted small" style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <Clock size={14} aria-hidden="true" />
        <a
          href={mirror.origin}
          className="mono"
          target="_blank"
          rel="noopener noreferrer"
          title={mirror.origin}
        >
          {mirror.origin}
        </a>
      </span>
      {mirror.lastSyncedAt ? (
        <span className="muted small" style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          · {timeAgo(mirror.lastSyncedAt)}
        </span>
      ) : null}
    </span>
  );
}

/** Banner informativo nel Code page — varianti: active, paused, diverged, stale. */
export function PullMirrorInfoBanner({ mirror }: { mirror: PullMirror | null }) {
  if (!mirror) return null;
  const v = mirrorVariant(mirror);
  const isWrite = mirror.mode === 'once' || mirror.state === 'stopped';

  // Banner principale: spiegazione del mirror.
  if (v === 'active' || v === 'paused') {
    return (
      <div className="alert alert-info" role="note">
        <Info size={16} aria-hidden="true" />
        <div>
          This repository is a <b>pull mirror</b>: pushes are rejected here.{' '}
          {!isWrite && mirror.origin ? (
            <>
              Push to <span className="mono">{mirror.origin}</span>; changes appear here at the next update.
            </>
          ) : null}{' '}
          Issues, labels and milestones work as usual.
          {v === 'paused' && <span style={{ color: 'var(--warning)', fontWeight: 600 }}> (paused)</span>}
        </div>
      </div>
    );
  }

  if (v === 'diverged') {
    return (
      <div className="alert alert-warning" role="alert">
        <ShieldAlert size={16} aria-hidden="true" />
        <div>
          <b>Some refs have diverged.</b> The source has been rewritten or deleted and GitStack could not fast-forward.
          {mirror.divergedRefs.length > 0 ? (
            <>
              {' '}Diverged: <span className="mono">{mirror.divergedRefs.map((r) => r.ref).join(', ')}</span>.
            </>
          ) : null}{' '}
          Nothing was overwritten.
        </div>
      </div>
    );
  }

  // Stale: nessun aggiornamento riuscito da oltre 24h o fermo.
  const sinceLast = mirror.lastSyncedAt ? `Last success ${timeAgo(mirror.lastSyncedAt)}.` : 'The mirror has never synced.';
  const errorMsg = mirror.lastError ? ` Last error: ${mirror.lastError}.` : '';
  return (
    <div className="alert alert-danger" role="alert">
      <ShieldAlert size={16} aria-hidden="true" />
      <div>
        <b>No successful update for 26 hours.</b> {sinceLast}{errorMsg} The token may have expired. Repo admins were notified.
      </div>
    </div>
  );
}
