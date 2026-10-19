import { CircleCheck, CircleX, Clock } from 'lucide-react';

// Stato di sintesi dei commit (PR9). `none` non mostra nulla: il commit non ha stati.
export type CommitState = 'none' | 'pending' | 'success' | 'failure' | 'error';

const LABEL: Record<Exclude<CommitState, 'none'>, string> = {
  pending: 'Pending',
  success: 'Success',
  failure: 'Failure',
  error: 'Error',
};

// Stato = colore + icona + testo (design system: mai solo colore).
export function CommitStatusBadge({ state, label }: { state: CommitState; label?: string }) {
  if (state === 'none') return null;
  const text = label ?? LABEL[state];
  if (state === 'success') {
    return (
      <span className="badge badge-open">
        <CircleCheck size={13} aria-hidden="true" />
        {text}
      </span>
    );
  }
  if (state === 'pending') {
    return (
      <span className="badge" style={{ color: 'var(--warning)', borderColor: 'var(--warning)' }}>
        <Clock size={13} aria-hidden="true" />
        {text}
      </span>
    );
  }
  return (
    <span className="badge badge-danger">
      <CircleX size={13} aria-hidden="true" />
      {text}
    </span>
  );
}
