-- Rollback di 0005: la cronologia torna ordinata solo per created_at.
DROP INDEX IF EXISTS core.issue_events_issue_seq_idx;
ALTER TABLE core.issue_events DROP COLUMN IF EXISTS seq;
