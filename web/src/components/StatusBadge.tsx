import { Bot, CircleCheck, CircleDot, CircleX, Lock } from 'lucide-react';

// Stato = colore + icona + testo (design system: mai solo colore).
const STATUS = {
  open: { label: 'Open', cls: 'badge-open', Icon: CircleDot },
  closed: { label: 'Closed', cls: 'badge-closed', Icon: CircleCheck },
  agent: { label: 'agent', cls: 'badge-agent', Icon: Bot },
  private: { label: 'Private', cls: '', Icon: Lock },
  error: { label: 'Error', cls: 'badge-danger', Icon: CircleX },
} as const;

export type Status = keyof typeof STATUS;

export function StatusBadge({ status, label }: { status: Status; label?: string }) {
  const { label: defaultLabel, cls, Icon } = STATUS[status];
  return (
    <span className={['badge', cls].filter(Boolean).join(' ')}>
      <Icon size={13} aria-hidden="true" />
      {label ?? defaultLabel}
    </span>
  );
}
