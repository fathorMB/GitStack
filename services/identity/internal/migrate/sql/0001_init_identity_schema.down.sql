-- Rollback di 0001: rimuove solo cio' che la migrazione ha creato, in ordine
-- inverso di dipendenza. Non elimina lo schema "identity" (puo' averlo creato
-- un operatore, vedi migrations/bootstrap-role.sql).
DROP TABLE IF EXISTS identity.oidc_identities;
DROP TABLE IF EXISTS identity.oidc_providers;
DROP TABLE IF EXISTS identity.ssh_keys;
DROP TABLE IF EXISTS identity.api_tokens;
DROP TABLE IF EXISTS identity.sessions;
DROP TABLE IF EXISTS identity.credentials;
DROP TABLE IF EXISTS identity.users;
