import { AlertTriangle } from 'lucide-react';

// Errore dell'API in forma leggibile (role="alert"), stile .alert-danger.
export function ErrorAlert({ message }: { message: string }) {
  return (
    <div className="alert alert-danger" role="alert">
      <AlertTriangle size={16} aria-hidden="true" />
      <span>{message}</span>
    </div>
  );
}
