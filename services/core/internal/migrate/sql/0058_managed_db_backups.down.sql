-- GIT-553: il down toglie i metadati dei dump e le colonne di backup/ripristino/aggiornamento. I file dei
-- dump su databases-data non si toccano da qui (li elimina il controller con il database, DB19).
ALTER TABLE core.managed_dbs
    DROP COLUMN IF EXISTS restore_error,
    DROP COLUMN IF EXISTS restore_requested_by,
    DROP COLUMN IF EXISTS restore_mode,
    DROP COLUMN IF EXISTS restore_file,
    DROP COLUMN IF EXISTS restore_backup_id,
    DROP COLUMN IF EXISTS upgrade_error,
    DROP COLUMN IF EXISTS upgrade_requested_by,
    DROP COLUMN IF EXISTS upgrade_requested_at,
    DROP COLUMN IF EXISTS last_backup_at,
    DROP COLUMN IF EXISTS backup_error_kind,
    DROP COLUMN IF EXISTS backup_error;
DROP TABLE IF EXISTS core.managed_db_backups;
