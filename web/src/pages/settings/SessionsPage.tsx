import { Monitor } from 'lucide-react';
import { useState } from 'react';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, useToast } from '../../components';
import { endSession, fetchSessions } from '../../lib/accessApi';
import type { Session } from '../../lib/accessApi';
import { formatDate } from '../../lib/format';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

// Sessioni web attive (ASVS V3.3.4): elenco con user agent, IP e ultimo uso,
// e fine di una singola sessione. Terminare quella corrente equivale a un
// logout: la prossima chiamata risponde 401.
export function SessionsPage() {
  const { data, setData, loading, error } = useLoad(fetchSessions);
  const { toast } = useToast();
  const sessions = data ?? [];
  const [toEnd, setToEnd] = useState<Session | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function handleEnd(s: Session) {
    setActionError(null);
    try {
      await endSession(s.id);
      setData(sessions.filter((x) => x.id !== s.id));
      toast('Session ended.', 'success');
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <div>
      <div className="sec-h">
        <h2>Sessions</h2>
      </div>
      <p className="muted">
        Browsers signed in to your account. Sessions end after a period of inactivity; end any you do not recognise.
      </p>

      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}

      {loading && data === null ? (
        <p className="muted">Loading sessions…</p>
      ) : sessions.length === 0 ? (
        error ? null : (
          <EmptyState icon={<Monitor size={32} />} title="No active sessions" description="You are not signed in with a session." />
        )
      ) : (
        <ul className="list" aria-label="Sessions">
          {sessions.map((s) => (
            <li key={s.id} className="list-row">
              <Monitor size={16} className="muted" aria-hidden="true" />
              <div className="grow">
                <b>{s.userAgent || 'Unknown browser'}</b>
                {s.current ? <span className="badge"> This session</span> : null}
                <div className="mono small muted">
                  {s.ipAddress || 'unknown IP'} · {s.authMethod}
                </div>
              </div>
              <div className="small muted">
                Started {formatDate(s.createdAt)} · last active {formatDate(s.lastSeenAt)}
              </div>
              <Button
                size="sm"
                variant="danger"
                onClick={() => setToEnd(s)}
                aria-label={`End session ${s.userAgent || s.id}`}
              >
                End
              </Button>
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        open={toEnd !== null}
        onOpenChange={(open) => {
          if (!open) setToEnd(null);
        }}
        title="End session?"
        description={
          toEnd?.current
            ? 'This is the session you are using now: you will be signed out.'
            : 'The browser using this session will have to sign in again.'
        }
        confirmLabel="End session"
        destructive
        onConfirm={() => {
          if (toEnd) void handleEnd(toEnd);
        }}
      />
    </div>
  );
}
