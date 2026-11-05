-- v3-d (GIT-568, ADR 0003, DB20/DB21): telemetria dei database gestiti. I campioni di CPU/RAM
-- (metrics-server) e le righe di log di PostgreSQL (pods/log) si conservano come quelli delle app:
-- stessa scadenza (7 giorni) e stesso tetto di dimensione (1 GiB per database), potatura nel
-- controller. Un campione senza CPU ne' memoria vuol dire «sorgente non risponde»: l'API dichiara
-- available=false con serie vuote, mai zeri. Le righe seguono il pod: la sostituzione non perde
-- la cronologia, che vive qui e non nel pod. Tutto a cascata sul database: con l'eliminazione
-- (DB19) sparisce anche la telemetria.

CREATE TABLE core.managed_db_metric_samples (
    db_id UUID NOT NULL REFERENCES core.managed_dbs(id) ON DELETE CASCADE,
    at TIMESTAMPTZ NOT NULL,
    ready BOOLEAN NOT NULL,
    restarts INTEGER NOT NULL CHECK (restarts >= 0),
    -- metrics-server: NULL = la sorgente non ha risposto per questo campione.
    cpu_millicores INTEGER NULL CHECK (cpu_millicores IS NULL OR cpu_millicores >= 0),
    memory_bytes BIGINT NULL CHECK (memory_bytes IS NULL OR memory_bytes >= 0)
);
CREATE INDEX managed_db_metric_samples_db_at ON core.managed_db_metric_samples (db_id, at);

CREATE TABLE core.managed_db_log_lines (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    db_id UUID NOT NULL REFERENCES core.managed_dbs(id) ON DELETE CASCADE,
    at TIMESTAMPTZ NOT NULL,
    message TEXT NOT NULL,
    bytes INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0)
);
CREATE INDEX managed_db_log_lines_db_at ON core.managed_db_log_lines (db_id, at, id);

-- Il webhook database (DB20) entra nell'elenco degli eventi selezionabili (come deployment della 0044 e
-- app_domain della 0054): si configura sul webhook dell'organizzazione proprietaria del database.
ALTER TABLE core.webhooks DROP CONSTRAINT webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment','app_domain','database']::TEXT[]);
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment','app_domain','database'));
