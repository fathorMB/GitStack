-- Rollback di 0003_repositories_owner_name.
DROP INDEX IF EXISTS core.repositories_owner_name_idx;
ALTER TABLE core.repositories DROP COLUMN IF EXISTS owner_name;
