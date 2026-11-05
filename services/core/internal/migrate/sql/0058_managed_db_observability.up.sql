-- v3-d (GIT-551, DB21): campioni di CPU/RAM/dimensione e log di PostgreSQL conservati nel database di core,
-- cosi' sopravvivono alla sostituzione del pod. cpu/memory NULL = metrics-server non ha risposto
-- (l'API lo dice «non disponibile», non mostra zeri).
CREATE TABLE core.managed_db_metrics (
    db_id UUID NOT NULL REFERENCES core.managed_dbs (id) ON DELETE CASCADE,
    at TIMESTAMPTZ NOT NULL,
    cpu_millis INTEGER NULL CHECK (cpu_millis IS NULL OR cpu_millis >= 0),
    memory_bytes BIGINT NULL CHECK (memory_bytes IS NULL OR memory_bytes >= 0),
    db_bytes BIGINT NULL CHECK (db_bytes IS NULL OR db_bytes >= 0),
    PRIMARY KEY (db_id, at)
);

CREATE TABLE core.managed_db_logs (
    id BIGSERIAL PRIMARY KEY,
    db_id UUID NOT NULL REFERENCES core.managed_dbs (id) ON DELETE CASCADE,
    at TIMESTAMPTZ NOT NULL,
    message TEXT NOT NULL
);
CREATE INDEX managed_db_logs_db_idx ON core.managed_db_logs (db_id, id);
