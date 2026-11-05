-- v3-d (GIT-553, ADR 0003, DB6, DB13, DB15): backup e ripristino dei database gestiti.
-- Un dump e' un file cifrato (AES-256-GCM, chiave di dump dell'istanza) su databases-data di core; qui
-- stanno solo i metadati: mai contenuti del dump, mai password.

CREATE TABLE core.managed_db_backups (
    id UUID PRIMARY KEY,
    db_id UUID NOT NULL REFERENCES core.managed_dbs (id) ON DELETE CASCADE,
    -- scheduled: giornaliero (ultimi 7). on_demand: su richiesta (ultimi 5). safety: dump di sicurezza
    -- prima di un ripristino sul posto o di un aggiornamento minor (ultimi 3).
    kind TEXT NOT NULL CHECK (kind IN ('scheduled', 'on_demand', 'safety')),
    status TEXT NOT NULL CHECK (status IN ('in_progress', 'completed', 'failed')),
    size_bytes BIGINT NULL CHECK (size_bytes IS NULL OR size_bytes >= 0),
    error TEXT NULL,
    -- Percorso del file dentro la cartella dei dump di core (relativo, mai fuori da essa).
    file TEXT NULL,
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL
);
CREATE INDEX managed_db_backups_db_idx ON core.managed_db_backups (db_id, kind, created_at DESC);
-- Un solo dump alla volta per database (409 backup_in_progress).
CREATE UNIQUE INDEX managed_db_backups_one_running ON core.managed_db_backups (db_id) WHERE status = 'in_progress';

ALTER TABLE core.managed_dbs
    -- Ultimo errore di backup, visibile nel database e in `gitstack status`; si azzera col primo dump riuscito
    -- dello stesso tipo.
    ADD COLUMN backup_error TEXT NULL,
    ADD COLUMN backup_error_kind TEXT NULL,
    ADD COLUMN last_backup_at TIMESTAMPTZ NULL,
    -- Aggiornamento minor su richiesta (DB6): richiesta pendente ed esito dell'ultima.
    ADD COLUMN upgrade_requested_at TIMESTAMPTZ NULL,
    ADD COLUMN upgrade_requested_by JSONB NULL,
    ADD COLUMN upgrade_error TEXT NULL,
    -- Ripristino pendente (DB15): dump sorgente (anche di un altro database dell'owner) e modo.
    ADD COLUMN restore_backup_id UUID NULL,
    ADD COLUMN restore_file TEXT NULL,
    ADD COLUMN restore_mode TEXT NULL CHECK (restore_mode IS NULL OR restore_mode IN ('same', 'new')),
    ADD COLUMN restore_requested_by JSONB NULL,
    ADD COLUMN restore_error TEXT NULL;
