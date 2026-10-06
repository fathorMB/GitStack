-- Rollback di 0010: il motore delle notifiche torna a non sapere cosa ha letto.
DROP INDEX IF EXISTS core.event_outbox_unnotified;
ALTER TABLE core.event_outbox DROP COLUMN IF EXISTS notified_at;
