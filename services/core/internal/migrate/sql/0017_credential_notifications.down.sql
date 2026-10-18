-- Rollback di 0017: via le notifiche sulle credenziali.
DELETE FROM core.notifications WHERE reason = 'credential';
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror'));
