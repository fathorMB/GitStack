-- Rollback di 0023: le colonne via_token_*/by_agent degli stati di commit.
ALTER TABLE core.commit_statuses
    DROP COLUMN IF EXISTS via_token_id,
    DROP COLUMN IF EXISTS via_token_name,
    DROP COLUMN IF EXISTS by_agent;
