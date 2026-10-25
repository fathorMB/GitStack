-- v3-a (GIT-342, H5/H8): app di un repo, versioni (immagine + configurazione) e deploy.
-- L'app appartiene a un repo: se la riga del repo sparisce (purge dopo i 7 giorni del cestino,
-- R2) spariscono anche app, versioni e deploy. Archiviazione (R10) e cestino (R2) non toccano
-- queste righe: lo stato «ferma» si ricava dal repo, così al ripristino l'app torna com'era.

CREATE TABLE core.apps (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$'),
    -- Etichetta DNS dell'indirizzo automatico (H2), unica in tutta l'istanza; NULL senza porta.
    address_label TEXT NULL CHECK (address_label IS NULL OR char_length(address_label) BETWEEN 1 AND 63),
    -- Stato osservato dal controller (item di deploy); starting finché non lo aggiorna.
    status TEXT NOT NULL DEFAULT 'starting' CHECK (status IN ('starting', 'running', 'restarting', 'unhealthy', 'failed', 'stopped')),
    access_mode TEXT NOT NULL DEFAULT 'login' CHECK (access_mode IN ('login', 'none', 'public')),
    copies INTEGER NOT NULL DEFAULT 1 CHECK (copies >= 0),
    ready_copies INTEGER NOT NULL DEFAULT 0 CHECK (ready_copies >= 0),
    port INTEGER NULL CHECK (port IS NULL OR port BETWEEN 1 AND 65535),
    health_check JSONB NULL,
    follow_branch TEXT NULL,
    auto_deploy_paused BOOLEAN NOT NULL DEFAULT false,
    restricted BOOLEAN NOT NULL DEFAULT false,
    guaranteed_cpu_millis INTEGER NOT NULL CHECK (guaranteed_cpu_millis >= 1),
    guaranteed_memory_bytes BIGINT NOT NULL CHECK (guaranteed_memory_bytes >= 1),
    limit_cpu_millis INTEGER NOT NULL CHECK (limit_cpu_millis >= 1),
    limit_memory_bytes BIGINT NOT NULL CHECK (limit_memory_bytes >= 1),
    -- Versione che serve il traffico; il numero successivo si prende da next_version.
    active_version INTEGER NOT NULL DEFAULT 1,
    next_version INTEGER NOT NULL DEFAULT 2,
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT apps_repo_name UNIQUE (repo_id, name),
    CONSTRAINT apps_limit_ge_guaranteed CHECK (limit_cpu_millis >= guaranteed_cpu_millis AND limit_memory_bytes >= guaranteed_memory_bytes)
);
CREATE UNIQUE INDEX apps_address_label_key ON core.apps (address_label) WHERE address_label IS NOT NULL;

-- Una versione: immagine e configurazione congelate (H8). Mai modificata dopo la scrittura, tranne
-- state/state_reason che segue l'esito del deploy.
CREATE TABLE core.app_versions (
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK (number >= 1),
    trigger TEXT NOT NULL CHECK (trigger IN ('create', 'manual', 'branch_push', 'config', 'rollback')),
    state TEXT NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'serving', 'superseded', 'failed', 'rolled_back')),
    state_reason TEXT NULL,
    image_reference TEXT NOT NULL CHECK (char_length(image_reference) BETWEEN 1 AND 512),
    image_digest TEXT NULL CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
    image_source TEXT NOT NULL CHECK (image_source IN ('registry', 'external')),
    image_verified BOOLEAN NOT NULL DEFAULT false,
    signed_by TEXT NULL,
    runs_as_root BOOLEAN NULL,
    branch TEXT NULL,
    commit_sha TEXT NULL,
    summary TEXT NULL,
    rollback_of INTEGER NULL,
    config JSONB NOT NULL,
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (app_id, number)
);

-- Un tentativo di deploy di una versione (lo scrive il controller; qui stato e storico).
CREATE TABLE core.app_deploys (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL,
    version INTEGER NOT NULL,
    state TEXT NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'serving', 'superseded', 'failed', 'rolled_back')),
    reason TEXT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NULL,
    FOREIGN KEY (app_id, version) REFERENCES core.app_versions (app_id, number) ON DELETE CASCADE
);
CREATE INDEX app_deploys_app_idx ON core.app_deploys (app_id, started_at DESC);

-- Quota delle app di un owner (H5, H11), contata su tutti i suoi repo. Senza riga valgono i
-- default dell'istanza.
CREATE TABLE core.app_owner_quotas (
    owner_name TEXT PRIMARY KEY CHECK (owner_name = lower(owner_name)),
    cpu_millis INTEGER NOT NULL CHECK (cpu_millis >= 0),
    memory_bytes BIGINT NOT NULL CHECK (memory_bytes >= 0),
    disk_bytes BIGINT NOT NULL CHECK (disk_bytes >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
