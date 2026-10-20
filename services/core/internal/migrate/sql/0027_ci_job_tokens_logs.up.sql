-- v2-a (GIT-316): ci-gateway. Il protocollo runner.v1 identifica un task con un
-- intero (task_id), il token del job e' emesso quando il job e' preso e vale solo
-- per quel job, i log sono su disco e qui c'e' solo il contatore delle righe.

-- Numero del task per il protocollo runner.v1 (int64): mai riusato.
ALTER TABLE core.ci_jobs ADD COLUMN task_id BIGINT GENERATED ALWAYS AS IDENTITY;
CREATE UNIQUE INDEX ci_jobs_task_id_key ON core.ci_jobs (task_id);

-- Token del job: si conserva solo lo SHA-256. Scade pochi minuti dopo il tetto di
-- durata del job e viene revocato quando il job finisce.
CREATE TABLE core.ci_job_tokens (
    job_id UUID PRIMARY KEY REFERENCES core.ci_jobs (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL
);

-- Righe e byte scritti nel file di log del job (volume di core, fuori dai backup).
CREATE TABLE core.ci_job_logs (
    job_id UUID PRIMARY KEY REFERENCES core.ci_jobs (id) ON DELETE CASCADE,
    lines BIGINT NOT NULL DEFAULT 0 CHECK (lines >= 0),
    bytes BIGINT NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    truncated BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
