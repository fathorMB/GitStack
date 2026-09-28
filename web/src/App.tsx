import { BrowserRouter, Route, Routes } from 'react-router-dom';
import { ToastProvider } from './components';
import { AppShell } from './layout/AppShell';
import { ComponentsPage } from './pages/ComponentsPage';
import { LoginPage } from './pages/LoginPage';
import { ResourcesPage } from './pages/ResourcesPage';
import { AuthProvider, RequireAuth } from './routes/auth';

// Rotte: /login (segnaposto, GIT-40 lo sostituisce) e le pagine protette, che
// dopo un 401 dal client generato rimandano a /login. /_components e' la
// pagina interna di revisione del design system: fuori dalla sidebar.
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <AppShell crumb="Resources">
              <ResourcesPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/_components"
        element={
          <RequireAuth>
            <AppShell crumb="Components">
              <ComponentsPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  );
}

function NotFoundPage() {
  return (
    <AppShell crumb="Not found">
      <p className="muted">Page not found.</p>
    </AppShell>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <AppRoutes />
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  );
}
