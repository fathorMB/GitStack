import type { ReactNode } from 'react';
import { Sidebar } from './Sidebar';
import { Topbar } from './Topbar';

// Scheletro dell'app: sidebar (232px) + topbar (52px) + contenuto, come
// design/mockups-v1/index.html (.app/.sidebar/.main/.topbar/.content) e
// design/styleguide/index.html (sezione "Layout console").
export function AppShell({ crumb, children }: { crumb: string; children: ReactNode }) {
  return (
    <div className="app">
      <Sidebar />
      <div className="main">
        <Topbar crumb={crumb} />
        <div className="content">
          <div className="page">{children}</div>
        </div>
      </div>
    </div>
  );
}
