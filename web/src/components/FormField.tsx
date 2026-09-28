import { Label } from '@radix-ui/react-label';
import { cloneElement, isValidElement, useId } from 'react';
import type { ReactElement, ReactNode } from 'react';

export interface FormFieldProps {
  label: string;
  hint?: string;
  /** Messaggio di errore del campo: se presente il controllo diventa aria-invalid. */
  error?: string;
  children: ReactElement<Record<string, unknown>>;
}

// Etichetta + controllo + hint + errore: collega label (htmlFor), hint ed
// errore (aria-describedby) al controllo figlio, che riceve id/aria-*.
export function FormField({ label, hint, error, children }: FormFieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : '', error ? errorId : ''].filter(Boolean).join(' ') || undefined;
  const control: ReactNode = isValidElement(children)
    ? cloneElement(children, { id, 'aria-invalid': error ? true : undefined, 'aria-describedby': describedBy })
    : children;
  return (
    <div className="field">
      <Label htmlFor={id}>{label}</Label>
      {control}
      {hint ? (
        <span id={hintId} className="hint">
          {hint}
        </span>
      ) : null}
      {error ? (
        <span id={errorId} className="error" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}
