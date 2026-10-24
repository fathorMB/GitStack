-- v2-a (GIT-280, CI5): artefatti, cache e valori regolabili dall'amministratore.
-- I byte stanno su disco o su S3 (storage_key); qui solo i metadati.

-- Impostazioni di conservazione e limiti: una sola riga, creata al primo salvataggio.
-- Senza riga valgono i default di CI5 (log 90 giorni, artefatti 30 giorni fino a 2 GB,
-- cache 10 GB per repo, inutilizzate da 7 giorni).
CREATE TABLE core.ci_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    log_retention_days INTEGER NOT NULL DEFAULT 90 CHECK (log_retention_days BETWEEN 1 AND 3650),
    artifact_retention_days INTEGER NOT NULL DEFAULT 30 CHECK (artifact_retention_days BETWEEN 1 AND 3650),
    artifact_max_bytes BIGINT NOT NULL DEFAULT 2147483648 CHECK (artifact_max_bytes >= 1),
    cache_quota_bytes BIGINT NOT NULL DEFAULT 10737418240 CHECK (cache_quota_bytes >= 1),
    cache_unused_days INTEGER NOT NULL DEFAULT 7 CHECK (cache_unused_days BETWEEN 1 AND 3650),
    updated_by UUID NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Un artefatto di un'esecuzione (nome unico nell'esecuzione, come upload-artifact v4).
CREATE TABLE core.ci_artifacts (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES core.ci_runs (id) ON DELETE CASCADE,
    job_id UUID NULL REFERENCES core.ci_jobs (id) ON DELETE SET NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    storage_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ci_artifacts_run_name UNIQUE (run_id, name)
);
CREATE INDEX ci_artifacts_expires_idx ON core.ci_artifacts (expires_at);
CREATE INDEX ci_artifacts_repo_idx ON core.ci_artifacts (repo_id);

-- Una cache di un repo. seq e' il cacheId intero del protocollo di actions/cache v1.
-- ready_at NULL: prenotata, upload in corso (non si restituisce e non conta nella quota).
CREATE TABLE core.ci_caches (
    id UUID PRIMARY KEY,
    seq BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    cache_key TEXT NOT NULL CHECK (char_length(cache_key) BETWEEN 1 AND 512),
    version TEXT NOT NULL CHECK (char_length(version) <= 128),
    ref TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    storage_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL,
    ready_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_caches_scope UNIQUE (repo_id, cache_key, version, ref)
);
CREATE INDEX ci_caches_repo_used_idx ON core.ci_caches (repo_id, last_used_at);
