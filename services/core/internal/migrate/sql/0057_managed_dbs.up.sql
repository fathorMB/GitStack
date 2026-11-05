-- v3-d (GIT-551, ADR 0003, DB1-DB4, DB20): database PostgreSQL gestiti di un owner, collegamenti con le
-- app e traccia di audit. Le password NON stanno qui: vivono nel Secret del namespace del database
-- (e in quello dell'app per i link), mai nel database di core.

CREATE TABLE core.managed_dbs (
    id UUID PRIMARY KEY,
    -- Owner (utente o organizzazione) come in app_owner_quotas: minuscolo; owner_id e owner_type
    -- servono a identity per i permessi M-03 (DB2).
    owner_name TEXT NOT NULL CHECK (owner_name = lower(owner_name)),
    owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$'),
    pg_major INTEGER NOT NULL CHECK (pg_major IN (16, 17)),
    -- Dimensione dichiarata: limite morbido (ADR 0003 punto 3), mai applicata al PVC.
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 1),
    cpu_millis INTEGER NOT NULL CHECK (cpu_millis >= 1),
    memory_bytes BIGINT NOT NULL CHECK (memory_bytes >= 1),
    status TEXT NOT NULL DEFAULT 'creating' CHECK (status IN ('creating', 'running', 'restarting', 'updating', 'restoring', 'deleting', 'failed')),
    status_reason TEXT NULL,
    lan_enabled BOOLEAN NOT NULL DEFAULT false,
    lan_port INTEGER NULL CHECK (lan_port IS NULL OR lan_port BETWEEN 1 AND 65535),
    -- Immagine fissata per digest e verificata con cosign prima dello StatefulSet (DB5).
    image_digest TEXT NULL CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
    image_verified_at TIMESTAMPTZ NULL,
    -- Uso osservato da pg_database_size e stato del limite morbido (80% warning, 100% full).
    usage_bytes BIGINT NULL CHECK (usage_bytes IS NULL OR usage_bytes >= 0),
    disk_state TEXT NOT NULL DEFAULT 'none' CHECK (disk_state IN ('none', 'warning', 'full')),
    restarts INTEGER NOT NULL DEFAULT 0 CHECK (restarts >= 0),
    -- Generazione: sale a ogni modifica della specifica, il controller la riallinea.
    generation BIGINT NOT NULL DEFAULT 1,
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT managed_dbs_owner_name UNIQUE (owner_name, name)
);
CREATE UNIQUE INDEX managed_dbs_lan_port_key ON core.managed_dbs (lan_port) WHERE lan_port IS NOT NULL;

-- Collegamento app-database (DB9): un ruolo di login proprio per link. Le app stanno in core.apps
-- (SET NULL non basta: l'eliminazione dell'app scollega, lo fa il controller; qui CASCADE sul database).
CREATE TABLE core.managed_db_links (
    id UUID PRIMARY KEY,
    db_id UUID NOT NULL REFERENCES core.managed_dbs (id) ON DELETE CASCADE,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('rw', 'ro')),
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'unlinking')),
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT managed_db_links_app UNIQUE (db_id, app_id)
);
CREATE INDEX managed_db_links_app_idx ON core.managed_db_links (app_id);

-- Audit di ogni azione sui database (DB20): chi, cosa, quando, mai le password. owner_name e name
-- restano anche dopo l'eliminazione (db_id non ha chiave esterna).
CREATE TABLE core.managed_db_audit (
    id BIGSERIAL PRIMARY KEY,
    db_id UUID NOT NULL,
    owner_name TEXT NOT NULL,
    name TEXT NOT NULL,
    action TEXT NOT NULL,
    actor JSONB NULL,
    detail JSONB NULL,
    at TIMESTAMPTZ NOT NULL
);
CREATE INDEX managed_db_audit_db_idx ON core.managed_db_audit (db_id, at);
