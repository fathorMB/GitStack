import { Bell, Bot, Boxes, Database, GitBranch, Grid2x2, Home, Server, Users } from 'lucide-react';
import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';
import { Logo } from '../components';
import { useUnreadCount } from '../lib/useUnreadCount';

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
  to,
  count = 0,
}: {
  icon: ReactNode;
  label: string;
  active?: boolean;
  disabled?: boolean;
  soon?: boolean;
  to?: string;
  count?: number;
}) {
  const className = ['sb-item', active ? 'active' : '', disabled ? 'disabled' : ''].filter(Boolean).join(' ');
  if (to) {
    return (
      <NavLink to={to} end={to === '/'} className={({ isActive }) => `sb-item${isActive ? ' active' : ''}`}>
        {icon}
        <span>{label}</span>
        {count > 0 ? <span className="counter">{count}</span> : null}
      </NavLink>
    );
  }
  return (
    <button type="button" className={className} disabled={disabled} aria-current={active ? 'page' : undefined}>
      {icon}
      <span>{label}</span>
      {soon ? <span className="badge-soon">Soon</span> : null}
    </button>
  );
}

export function Sidebar() {
  const unread = useUnreadCount();
  return (
    <aside className="sidebar">
      <div className="sb-brand">
        <Logo />
        GitStack
      </div>

      <div className="sb-grp">Workspace</div>
      <SidebarItem icon={<Home size={18} strokeWidth={1.8} />} label="Home" disabled soon />
      <SidebarItem icon={<Boxes size={18} strokeWidth={1.8} />} label="Resources" to="/" />
      <SidebarItem icon={<GitBranch size={18} strokeWidth={1.8} />} label="Repositories" to="/repos" />
      <SidebarItem icon={<Bell size={18} strokeWidth={1.8} />} label="Notifications" to="/notifications" count={unread} />

      <div className="sb-grp">Platform</div>
      <SidebarItem icon={<Grid2x2 size={18} strokeWidth={1.8} />} label="Apps" disabled soon />
      <SidebarItem icon={<Database size={18} strokeWidth={1.8} />} label="Databases" disabled soon />

      <div className="sb-grp">Administration</div>
      <SidebarItem icon={<Users size={18} strokeWidth={1.8} />} label="Organization" to="/orgs" />
      <SidebarItem icon={<Bot size={18} strokeWidth={1.8} />} label="Agents" to="/admin/agents" />
      <SidebarItem icon={<Server size={18} strokeWidth={1.8} />} label="System" disabled soon />
    </aside>
  );
}
