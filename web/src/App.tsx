import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { ToastProvider } from './components';
import { AppShell } from './layout/AppShell';
import { AgentsPage } from './pages/admin/AgentsPage';
import { ChangePasswordPage } from './pages/ChangePasswordPage';
import { ComponentsPage } from './pages/ComponentsPage';
import { LoginPage } from './pages/LoginPage';
import { ResourcesPage } from './pages/ResourcesPage';
import { NewRepoPage } from "./pages/repos/NewRepoPage";
import { DeletedReposPage } from './pages/repos/DeletedReposPage';
import { RepoSettingsPage } from './pages/repos/RepoSettingsPage';
import { RepoPage } from "./pages/repos/RepoPage";
import { ReposPage } from "./pages/repos/ReposPage";
import { OrgPage } from './pages/orgs/OrgPage';
import { OrgsPage } from './pages/orgs/OrgsPage';
import { ProfilePage } from './pages/settings/ProfilePage';
import { SettingsLayout } from './pages/settings/SettingsLayout';
import { SshKeysPage } from './pages/settings/SshKeysPage';
import { TokensPage } from './pages/settings/TokensPage';
import { AuthProvider, RequireAuth } from './routes/auth';

// Rotte: /login e le pagine protette (risorse, impostazioni personali), che
// dopo un 401 dal client generato rimandano a /login. /_components e' la
// pagina interna di revisione del design system: fuori dalla sidebar.
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/change-password" element={<ChangePasswordPage />} />
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
      <Route
        path="/admin/agents"
        element={
          <RequireAuth>
            <AppShell crumb="Agents">
              <AgentsPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/orgs"
        element={
          <RequireAuth>
            <AppShell crumb="Organizations">
              <OrgsPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/orgs/:org"
        element={
          <RequireAuth>
            <AppShell crumb="Organization">
              <OrgPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/orgs/:org/deleted-repos"
        element={
          <RequireAuth>
            <AppShell crumb="Deleted repositories">
              <DeletedReposPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/settings"
        element={
          <RequireAuth>
            <AppShell crumb="Settings">
              <SettingsLayout />
            </AppShell>
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="profile" replace />} />
        <Route path="profile" element={<ProfilePage />} />
        <Route path="tokens" element={<TokensPage />} />
        <Route path="ssh-keys" element={<SshKeysPage />} />
        <Route path="deleted-repos" element={<DeletedReposPage />} />
      </Route>
      {/* Rotte dinamiche dopo quelle fisse: /:owner/:repo non copre /orgs, /settings... (due segmenti) e i nomi riservati (R1, GIT-64). */}
      <Route
        path="/repos"
        element={
          <RequireAuth>
            <AppShell crumb="Repositories">
              <ReposPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/new"
        element={
          <RequireAuth>
            <AppShell crumb="New repository">
              <NewRepoPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/tree/*"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/blob/*"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="blob" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/blame/*"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="blame" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/commits"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="commits" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/commits/*"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="commits" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/issues"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="issues" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/issues/new"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="newissue" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/issues/:number"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="issue" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/tags"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="tags" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/search"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="search" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/commit/:sha"
        element={
          <RequireAuth>
            <AppShell crumb="Repository">
              <RepoPage mode="commit" />
            </AppShell>
          </RequireAuth>
        }
      />
      <Route
        path="/:owner/:repo/settings"
        element={
          <RequireAuth>
            <AppShell crumb="Repository settings">
              <RepoSettingsPage />
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
