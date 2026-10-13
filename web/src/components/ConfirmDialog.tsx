import * as Dialog from '@radix-ui/react-dialog';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Button } from './Button';

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  /** Etichetta del pulsante (sempre visibile) */
  confirmLabel?: string;
  /** Testo che l'utente deve riscrivere per abilitare il pulsante. Se fornito, il pulsante è spento finché il valore non corrisponde. */
  confirmInput?: {
    placeholder: string;
    /** Valore atteso per abilitare il pulsante */
    expected: string;
  };
  cancelLabel?: string;
  destructive?: boolean;
  /** Callback chiamata al conferma. Riceve il valore inserito dall'utente (o undefined se senza confirmInput). */
  onConfirm: (value: string | undefined) => void;
}

// Dialog di conferma: focus trap, Esc, ritorno del focus e aria-labelledby /
// aria-describedby li gestisce Radix Dialog.
// Quando confirmInput è fornito, l'utente deve riscrivere il testo esatto
// prima che il pulsante di conferma sia abilitato (G8).
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = 'Confirm',
  confirmInput,
  cancelLabel = 'Cancel',
  destructive = false,
  onConfirm,
}: ConfirmDialogProps) {
  const [input, setInput] = useState('');

  useEffect(() => {
    if (open) setInput('');
  }, [open]);

  const match = confirmInput ? input === confirmInput.expected : true;

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="dialog">
          <Dialog.Title className="dialog-title">{title}</Dialog.Title>
          <Dialog.Description className="dialog-desc">{description}</Dialog.Description>
          {confirmInput && (
            <div style={{ marginTop: 12 }}>
              <label htmlFor="confirm-input" className="small muted">
                {confirmInput.placeholder}
              </label>
              <input
                id="confirm-input"
                type="text"
                className="mono"
                autoComplete="off"
                autoFocus
                placeholder={confirmInput.placeholder}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                style={{
                  width: '100%',
                  marginTop: 4,
                  padding: '6px 8px',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  fontSize: 'inherit',
                }}
              />
            </div>
          )}
          <div className="dialog-actions">
            <Dialog.Close asChild>
              <Button>{cancelLabel}</Button>
            </Dialog.Close>
            <Button
              variant={destructive ? 'danger' : 'primary'}
              disabled={!match}
              onClick={() => {
                onConfirm(input);
                onOpenChange(false);
              }}
            >
              {confirmLabel}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
