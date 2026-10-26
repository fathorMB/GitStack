-- v3-b (GIT-356, H3): volumi persistenti delle app. Un volume è un PVC di dimensione fissa
-- (storage class local-path di k3s) montato in un percorso del container. Le dimensioni contano
-- nella quota disco dell'owner (core.app_owner_quotas.disk_bytes). Ogni modifica crea una versione
-- di configurazione, che ne copia l'elenco in config.volumes. I volumi sono dati: un rollback
-- ripristina immagine e configurazione ma non toglie né rimette volumi, che restano quelli attuali.
CREATE TABLE core.app_volumes (
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$'),
    mount_path TEXT NOT NULL CHECK (char_length(mount_path) BETWEEN 2 AND 512 AND mount_path LIKE '/%'),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 1),
    version INTEGER NOT NULL CHECK (version >= 1),
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (app_id, name),
    CONSTRAINT app_volumes_mount UNIQUE (app_id, mount_path)
);
