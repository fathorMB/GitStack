import { ExternalLink } from 'lucide-react';
import { CommitStatusBadge } from '../../components/CommitStatusBadge';
import { safeExternalUrl } from '../../lib/commitView';
import type { CombinedCommitStatus } from '../../lib/pullsApi';

// Stati dei commit (PR9) nella vista commit: sintesi e un riga per contesto, con il link al sistema esterno.
export function CommitChecks({ status }: { status: CombinedCommitStatus }) {
  if (status.totalCount === 0 || status.statuses.length === 0) return null;
  const required = new Set(status.requiredContexts.map((r) => r.context));
  return (
    <section aria-label="Commit statuses" className="card" style={{ marginBottom: 18 }}>
      <div className="card-h">
        Checks
        <span className="sp" />
        <CommitStatusBadge state={status.state} />
      </div>
      <div className="list" style={{ border: 0, borderRadius: 0 }}>
        {status.statuses.map((s) => {
          const href = safeExternalUrl(s.targetUrl);
          return (
            <div key={s.id} className="list-row">
              <span className="mono small" style={{ minWidth: 0 }}>
                {s.context}
              </span>
              <CommitStatusBadge state={s.state} />
              {required.has(s.context) ? <span className="badge">required</span> : null}
              <span className="muted small" style={{ flex: 1, minWidth: 0 }}>
                {s.description ?? ''}
              </span>
              {href ? (
                <a className="small" href={href} target="_blank" rel="noopener noreferrer">
                  Details
                  <ExternalLink size={12} aria-hidden="true" style={{ marginLeft: 4 }} />
                  <span className="sr-only"> (opens the external system)</span>
                </a>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}
