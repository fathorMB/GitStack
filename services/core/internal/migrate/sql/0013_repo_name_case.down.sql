-- Rollback di 0013: fallisce se esistono nomi con maiuscole (vanno rinominati prima).
DROP INDEX IF EXISTS core.repositories_owner_name_lower_idx;
DROP INDEX IF EXISTS core.repositories_owner_lower_name_key;
ALTER TABLE core.repositories DROP CONSTRAINT repositories_name_check;
ALTER TABLE core.repositories ADD CONSTRAINT repositories_name_check
    CHECK (name ~ '^[a-z0-9_-][a-z0-9._-]{0,99}$' AND name NOT LIKE '%.git');
ALTER TABLE core.repositories ADD CONSTRAINT repositories_owner_name_key UNIQUE (owner_type, owner_id, name);
