import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { fetchSession } from '../lib/authApi';
import { ApiError } from '../lib/http';
import { installPasswordChangeHandler, installUnauthorizedHandler } from '../lib/unauthorized';

interface AuthState {
  /** true dopo un 401 dal gateway, finche' l'utente non passa dal login. */
  unauthorized: boolean;
  clearUnauthorized: () => void;
  /** true finche' la password iniziale non e' cambiata (403 password_change_required o mustChangePassword al login). */
  mustChangePassword: boolean;
  setMustChangePassword: (value: boolean) => void;
  /** true dopo che /auth/session ha risposto (anche 401): il gateway e' raggiungibile. */
  sessionVerified: boolean;
  setSessionVerified: (value: boolean) => void;
}

const AuthContext = createContext<AuthState | null>(null);

// Ascolta i 401 del client generato e li espone al router.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [unauthorized, setUnauthorized] = useState(false);
  useEffect(() => installUnauthorizedHandler(() => setUnauthorized(true)), []);
  const [mustChangePassword, setMustChangePassword] = useState(false);
  useEffect(() => installPasswordChangeHandler(() => setMustChangePassword(true)), []);
  const [sessionVerified, setSessionVerified] = useState(false);
  const clearUnauthorized = useCallback(() => setUnauthorized(false), []);
  const value = useMemo(
    () => ({ unauthorized, clearUnauthorized, mustChangePassword, setMustChangePassword, sessionVerified, setSessionVerified }),
    [unauthorized, clearUnauthorized, mustChangePassword, sessionVerified],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth fuori da <AuthProvider>');
  return ctx;
}

// Un 404, un 5xx o un errore di rete su /auth/session non e' un 401: il
// servizio non risponde (Ingress sbagliato, gateway giu'), non "non sei loggato"
// ne' "sei loggato". Ogni altra risposta (200, 401, 403...) vuol dire che il
// gateway c'e': 401 e password_change_required li gestiscono gli interceptor.
// eslint-disable-next-line react-refresh/only-export-components
export function isServiceUnreachable(err: unknown): boolean {
  if (err instanceof ApiError) return err.status === undefined || err.status === 404 || err.status >= 500;
  return true;
}

type SessionCheck = 'checking' | 'ok' | 'unreachable';

// Rotta protetta: prima verifica la sessione (una volta, poi si ricorda), dopo
// un 401 rimanda a /login ricordando dove si voleva andare.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { unauthorized, mustChangePassword, sessionVerified, setSessionVerified } = useAuth();
  const location = useLocation();
  const [check, setCheck] = useState<SessionCheck>(sessionVerified ? 'ok' : 'checking');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (sessionVerified) return;
    let live = true;
    const done = (err?: unknown) => {
      if (!live) return;
      const down = err !== undefined && isServiceUnreachable(err);
      setCheck(down ? 'unreachable' : 'ok');
      if (!down) setSessionVerified(true);
    };
    fetchSession().then(() => done(), (err: unknown) => done(err ?? new Error('unreachable')));
    return () => {
      live = false;
    };
  }, [attempt, sessionVerified, setSessionVerified]);
  if (unauthorized) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  if (mustChangePassword) {
    return <Navigate to="/change-password" replace state={{ from: location.pathname + location.search }} />;
  }
  if (check === 'unreachable') {
    return (
      <main role="alert" style={{ padding: '3rem 1.5rem', textAlign: 'center' }}>
        <h1>Service unreachable</h1>
        <p>GitStack cannot reach its server right now. Check your connection or try again in a moment.</p>
        <button
          type="button"
          onClick={() => {
            setCheck('checking');
            setAttempt((n) => n + 1);
          }}
        >
          Try again
        </button>
      </main>
    );
  }
  if (check === 'checking') {
    return <p role="status" style={{ padding: '3rem 1.5rem', textAlign: 'center' }}>Checking your session…</p>;
  }
  return <>{children}</>;
}
