-- Rollback documentato di 0001_init_core_schema: rimuove solo ciò che
-- questa migrazione ha creato (indice e tabella). Non elimina lo schema
-- "core": può essere stato creato da un operatore esterno (bootstrap-role
-- .sql) e altre migrazioni future potrebbero già presupporne la sola
-- esistenza. Per rimuovere anche lo schema, in ambienti usa e getta
-- (es. VM di test): DROP SCHEMA core;
DROP INDEX IF EXISTS core.resources_type_idx;
DROP TABLE IF EXISTS core.resources;
