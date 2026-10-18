-- Rollback di 0019: si perdono la cronologia delle PR e l'origine via token.
DROP INDEX IF EXISTS core.pull_requests_open_branches_key;
DROP TABLE IF EXISTS core.pull_request_events;
ALTER TABLE core.pull_requests
    DROP COLUMN IF EXISTS via_token_name,
    DROP COLUMN IF EXISTS via_token_id;
