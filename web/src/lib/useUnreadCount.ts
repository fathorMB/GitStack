import { useCallback, useEffect, useState } from 'react';
import { fetchUnreadCount, onNotificationsChanged } from './notificationsApi';

// Non lette per Topbar e Sidebar; resta a zero, senza errori, se il backend
// risponde 501 o e' assente. Si aggiorna quando la casella cambia.
export function useUnreadCount(): number {
  const [unread, setUnread] = useState(0);
  const refresh = useCallback(() => {
    fetchUnreadCount().then(setUnread, () => undefined);
  }, []);
  useEffect(() => {
    refresh();
    return onNotificationsChanged(refresh);
  }, [refresh]);
  return unread;
}
