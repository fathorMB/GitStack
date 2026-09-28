import { Eye, EyeOff } from 'lucide-react';
import { useState } from 'react';
import type { InputHTMLAttributes } from 'react';

export function TextInput({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={['input', className ?? ''].filter(Boolean).join(' ')} {...rest} />;
}

// Campo password con pulsante mostra/nascondi (etichettato, aria-pressed).
export function PasswordInput({ className, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  const [shown, setShown] = useState(false);
  return (
    <div className="input-wrap">
      <input
        className={['input', 'input-has-action', className ?? ''].filter(Boolean).join(' ')}
        type={shown ? 'text' : 'password'}
        {...rest}
      />
      <button
        type="button"
        className="input-action"
        aria-label={shown ? 'Hide password' : 'Show password'}
        aria-pressed={shown}
        onClick={() => setShown((s) => !s)}
      >
        {shown ? <EyeOff size={16} aria-hidden="true" /> : <Eye size={16} aria-hidden="true" />}
      </button>
    </div>
  );
}
