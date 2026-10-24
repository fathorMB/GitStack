-- v2-a (GIT-278): runner esterni registrati con token (CI2, ADR 0001 §4).
-- Nel database non finisce mai un token in chiaro: solo lo SHA-256.

-- Token di registrazione: lo crea l'amministratore, vale una volta sola e scade.
CREATE TABLE core.ci_runner_registration_tokens (
    id UUID PRIMARY KEY,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_by UUID NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL
);

-- Runner esterni. Il token del runner (FetchTask) e' emesso alla registrazione.
-- Revocare un runner = cancellare la riga: il suo token non si risolve piu'.
CREATE TABLE core.ci_runners (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    labels TEXT[] NOT NULL DEFAULT '{}',
    version TEXT NOT NULL DEFAULT '',
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NULL,
    CONSTRAINT ci_runners_name_key UNIQUE (name)
);

-- Le etichette di runs-on del job (minuscole) e il runner esterno che lo esegue.
ALTER TABLE core.ci_jobs ADD COLUMN runs_on TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE core.ci_jobs ADD COLUMN runner_id UUID NULL REFERENCES core.ci_runners (id) ON DELETE SET NULL;
CREATE INDEX ci_jobs_runner_idx ON core.ci_jobs (runner_id) WHERE runner_id IS NOT NULL AND status = 'in_progress';
