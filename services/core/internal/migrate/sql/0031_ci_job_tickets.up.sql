-- v2-a (GIT-277): la credenziale con cui un pod ritira il proprio job e'
-- monouso e legata a quel job. Nel database non finisce mai il valore in chiaro.
CREATE TABLE core.ci_job_tickets (
    job_id UUID PRIMARY KEY REFERENCES core.ci_jobs (id) ON DELETE CASCADE,
    ticket_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(ticket_hash) = 32),
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL
);
