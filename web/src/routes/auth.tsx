import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { installUnauthorizedHandler } from '../lib/unauthorized';

interface AuthState {
  /** true dopo un 401 dal gateway, finche' l'utente non passa dal login. */
  unauthorized: boolean;
  clearUnauthorized: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

// Ascolta i 401 del client generato e li espone al router.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [unauthorized, setUnauthorized] = useState(false);
  useEffect(() => installUnauthorizedHandler(() => setUnauthorized(true)), []);
  const clearUnauthorized = useCallback(() => setUnauthorized(false), []);
  const value = useMemo(() => ({ unauthorized, clearUnauthorized }), [unauthorized, clearUnauthorized]);
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
  const { unauthorized } = useAuth();
  const location = useLocation();
  if (unauthorized) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  return <>{children}</>;
}
