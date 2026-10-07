-- Rollback di 0014: via i mirror e le loro notifiche.
DELETE FROM core.notifications WHERE reason = 'mirror';
DELETE FROM core.notification_preferences WHERE reason = 'mirror';
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_mirror_matches_reason;
ALTER TABLE core.notifications DROP COLUMN IF EXISTS mirror_id;
ALTER TABLE core.notification_preferences DROP CONSTRAINT notification_preferences_reason_check;
ALTER TABLE core.notification_preferences ADD CONSTRAINT notification_preferences_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook'));
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook'));
DROP TABLE IF EXISTS core.repo_mirror_runs;
DROP TABLE IF EXISTS core.repo_mirrors;
