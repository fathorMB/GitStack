import { AppShell } from './layout/AppShell';
import { ResourcesPage } from './pages/ResourcesPage';

// Un'unica pagina in M-01 (T-07): quando arriveranno le pagine di prodotto
// vere (repository, issue...) qui ci sarà un router; per ora la shell basta
// a mostrare layout e navigazione (vedi Sidebar).
export function App() {
  return (
    <AppShell crumb="Resources">
      <ResourcesPage />
    </AppShell>
  );
}
