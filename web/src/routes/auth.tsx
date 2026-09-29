import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { installPasswordChangeHandler, installUnauthorizedHandler } from '../lib/unauthorized';

interface AuthState {
  /** true dopo un 401 dal gateway, finche' l'utente non passa dal login. */
  unauthorized: boolean;
  clearUnauthorized: () => void;
  /** true finche' la password iniziale non e' cambiata (403 password_change_required o mustChangePassword al login). */
  mustChangePassword: boolean;
  setMustChangePassword: (value: boolean) => void;
}

const AuthContext = createContext<AuthState | null>(null);

// Ascolta i 401 del client generato e li espone al router.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [unauthorized, setUnauthorized] = useState(false);
  useEffect(() => installUnauthorizedHandler(() => setUnauthorized(true)), []);
  const [mustChangePassword, setMustChangePassword] = useState(false);
  useEffect(() => installPasswordChangeHandler(() => setMustChangePassword(true)), []);
  const clearUnauthorized = useCallback(() => setUnauthorized(false), []);
  const value = useMemo(
    () => ({ unauthorized, clearUnauthorized, mustChangePassword, setMustChangePassword }),
    [unauthorized, clearUnauthorized, mustChangePassword],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth fuori da <AuthProvider>');
  return ctx;
}

// Rotta protetta: dopo un 401 rimanda a /login ricordando dove si voleva andare.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { unauthorized, mustChangePassword } = useAuth();
  const location = useLocation();
  if (unauthorized) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  if (mustChangePassword) {
    return <Navigate to="/change-password" replace state={{ from: location.pathname + location.search }} />;
  }
  return <>{children}</>;
}
