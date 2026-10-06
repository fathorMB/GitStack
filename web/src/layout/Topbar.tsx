import { Bell, LogOut, Settings } from 'lucide-react';
import { Link, useNavigate } from 'react-router-dom';
import { signOut } from '../lib/authApi';
import { useUnreadCount } from '../lib/useUnreadCount';

// Barra superiore (52px), stessa altezza e stile di
// design/mockups-v1/index.html (.topbar): breadcrumb a sinistra, a destra
// impostazioni personali e uscita.
export function Topbar({ crumb }: { crumb: string }) {
  const navigate = useNavigate();
  const unread = useUnreadCount();

  async function handleSignOut() {
    try {
      await signOut();
    } catch {
      // Sessione gia' scaduta o rete assente: si torna comunque al login.
    }
    navigate('/login', { replace: true });
  }

  return (
    <header className="topbar">
      <div className="crumb">
        <b>{crumb}</b>
      </div>
      <span className="sp" />
      <Link className="btn btn-ghost btn-sm" to="/notifications" aria-label={unread > 0 ? `Notifications, ${unread} unread` : 'Notifications'}>
        <Bell size={14} aria-hidden="true" />
        {unread > 0 ? <span className="counter">{unread}</span> : null}
      </Link>
      <Link className="btn btn-ghost btn-sm" to="/settings/profile">
        <Settings size={14} aria-hidden="true" />
        Settings
      </Link>
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => void handleSignOut()}>
        <LogOut size={14} aria-hidden="true" />
        Sign out
      </button>
    </header>
  );
}
