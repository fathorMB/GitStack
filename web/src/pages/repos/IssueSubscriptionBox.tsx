import { Bell, BellOff } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button, ErrorAlert } from '../../components';
import { describeError } from '../../lib/http';
import { fetchIssueSubscription, subscribeToIssue, unsubscribeFromIssue } from '../../lib/notificationsApi';
import type { IssueSubscription } from '../../lib/notificationsApi';

const REASONS: Record<IssueSubscription['reason'], string> = {
  author: "you're the author",
  assignee: "you're assigned",
  commenter: "you commented",
  mentioned: "you were mentioned",
  manual: 'you subscribed',
  none: '',
};

// Barra laterale della issue (mockup 13, parte M-06): Subscribe/Unsubscribe e motivo (C3).
export function IssueSubscriptionBox({ owner, repo, number }: { owner: string; repo: string; number: number }) {
  const [sub, setSub] = useState<IssueSubscription | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    setSub(null);
    fetchIssueSubscription(owner, repo, number).then(
      (s) => live && setSub(s),
      (err: unknown) => live && setError(describeError(err)),
    );
    return () => {
      live = false;
    };
  }, [owner, repo, number]);

  const toggle = async () => {
    if (!sub) return;
    setBusy(true);
    setError(null);
    try {
      setSub(sub.subscribed ? await unsubscribeFromIssue(owner, repo, number) : await subscribeToIssue(owner, repo, number));
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="side-sec">
      <h4>Notifications</h4>
      {error ? <ErrorAlert message={error} /> : null}
      {sub ? (
        <>
          <Button size="sm" style={{ width: '100%', justifyContent: 'center' }} disabled={busy} onClick={() => void toggle()}>
            {sub.subscribed ? <BellOff size={14} aria-hidden="true" /> : <Bell size={14} aria-hidden="true" />}
            {sub.subscribed ? 'Unsubscribe' : 'Subscribe'}
          </Button>
          <p className="small muted" style={{ marginTop: 6 }}>
            {sub.subscribed
              ? `You're receiving notifications because ${REASONS[sub.reason] || 'you subscribed'}.`
              : "You're not receiving notifications for this issue."}
          </p>
        </>
      ) : null}
    </div>
  );
}
