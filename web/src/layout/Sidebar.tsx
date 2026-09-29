import { Boxes, Database, Grid2x2, Home, Server, Users } from 'lucide-react';
import type { ReactNode } from 'react';
import { Logo } from '../components';

// Struttura a gruppi (Workspace / Platform / Administration) e voci
// disattivate con etichetta "Soon" identiche a design/mockups-v1/index.html
// (schermata 02, sezione .sidebar): "la barra laterale ospita già le
// sezioni future ... si aggiungono voci, non si ridisegna" (styleguide,
// principio 3). Solo "Resources" è attivo in M-01: è la pagina di prova di
// T-07 (risorsa generica, D15), le altre voci del prodotto vero (repository,
// issue...) arrivano dopo, quindi restano disabilitate invece di puntare a
// pagine finte.
function SidebarItem({
  icon,
  label,
  active = false,
  disabled = false,
  soon = false,
}: {
  icon: ReactNode;
  label: string;
  active?: boolean;
  disabled?: boolean;
  soon?: boolean;
}) {
  const className = ['sb-item', active ? 'active' : '', disabled ? 'disabled' : ''].filter(Boolean).join(' ');
  return (
    <button type="button" className={className} disabled={disabled} aria-current={active ? 'page' : undefined}>
      {icon}
      <span>{label}</span>
      {soon ? <span className="badge-soon">Soon</span> : null}
    </button>
  );
}

export function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sb-brand">
        <Logo />
        GitStack
      </div>

      <div className="sb-grp">Workspace</div>
      <SidebarItem icon={<Home size={18} strokeWidth={1.8} />} label="Home" disabled soon />
      <SidebarItem icon={<Boxes size={18} strokeWidth={1.8} />} label="Resources" active />

      <div className="sb-grp">Platform</div>
      <SidebarItem icon={<Grid2x2 size={18} strokeWidth={1.8} />} label="Apps" disabled soon />
      <SidebarItem icon={<Database size={18} strokeWidth={1.8} />} label="Databases" disabled soon />

      <div className="sb-grp">Administration</div>
      <SidebarItem icon={<Users size={18} strokeWidth={1.8} />} label="Organization" disabled soon />
      <SidebarItem icon={<Server size={18} strokeWidth={1.8} />} label="System" disabled soon />
    </aside>
  );
}
