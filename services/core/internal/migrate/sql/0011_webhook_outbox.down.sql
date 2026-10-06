-- Rollback di 0011: il motore dei webhook torna a non sapere cosa ha letto.
DROP INDEX IF EXISTS core.event_outbox_unwebhooked;
ALTER TABLE core.event_outbox DROP COLUMN IF EXISTS webhooked_at;
