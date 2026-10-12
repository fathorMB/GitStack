-- Rollback di 0016: via le notifiche del mirror in pull e il loro legame.
DELETE FROM core.notifications WHERE pull_mirror_id IS NOT NULL;
ALTER TABLE core.notifications DROP CONSTRAINT IF EXISTS notifications_mirror_matches_reason;
ALTER TABLE core.notifications DROP COLUMN IF EXISTS pull_mirror_id;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_mirror_matches_reason CHECK ((reason = 'mirror') = (mirror_id IS NOT NULL));
