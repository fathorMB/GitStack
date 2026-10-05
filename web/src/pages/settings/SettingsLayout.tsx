import { Key, Trash2, Terminal, User as UserIcon } from 'lucide-react';
import { NavLink, Outlet, useOutletContext } from 'react-router-dom';
import { ErrorAlert } from '../../components';
import { fetchSession } from '../../lib/authApi';
import type { User } from '../../lib/authApi';
import { useLoad } from '../../lib/useLoad';

export interface SettingsContext {
  user: User;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useSettingsUser(): User {
  return useOutletContext<SettingsContext>().user;
}

function initials(user: User): string {
  return (user.displayName || user.username).slice(0, 2).toUpperCase();
}

// Impostazioni personali (mockup 14): sottomenu a sinistra (Profile / Access
// tokens / SSH keys), contenuto a destra. L'utente arriva da GET /auth/session.
export function SettingsLayout() {
  const { data: session, loading, error } = useLoad(fetchSession);

  return (
    <div className="settings-grid">
      <nav className="subnav" aria-label="Personal settings">
        <div className="row subnav-user">
          {session ? <span className="avatar avatar-lg">{initials(session.user)}</span> : null}
          <div>
            <b>{session?.user.username ?? 'Settings'}</b>
            <div className="small muted">Personal settings</div>
          </div>
        </div>
        <NavLink to="/settings/profile">
          <UserIcon size={16} aria-hidden="true" />
          Profile
        </NavLink>
        <div className="grp">Access</div>
        <NavLink to="/settings/tokens">
          <Key size={16} aria-hidden="true" />
          Access tokens
        </NavLink>
        <NavLink to="/settings/ssh-keys">
          <Terminal size={16} aria-hidden="true" />
          SSH keys
        </NavLink>
        <div className="grp">Repositories</div>
        <NavLink to="/settings/deleted-repos">
          <Trash2 size={16} aria-hidden="true" />
          Deleted repositories
        </NavLink>
      </nav>
      <div className="stack settings-main">
        {error ? <ErrorAlert message={error} /> : null}
        {loading && !session ? <p className="muted">Loading…</p> : null}
        {session ? <Outlet context={{ user: session.user } satisfies SettingsContext} /> : null}
      </div>
    </div>
  );
}
