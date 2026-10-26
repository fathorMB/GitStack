-- v3-a (GIT-384, H12): metriche minime delle app. Un campione per copia (pod) e per raccolta: stato
-- di pronto, riavvii cumulativi del container e, quando la sorgente risponde (metrics-server), CPU in
-- millicore e memoria in byte. Un campione senza CPU né memoria non è uno zero: la sorgente non ha
-- risposto. Le righe escono con l'app (ON DELETE CASCADE); la pulizia per età è in
-- store.PruneAppMetrics, con la stessa scadenza dei log (7 giorni).
CREATE TABLE core.app_metric_samples (
    id BIGSERIAL PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    copy TEXT NOT NULL CHECK (char_length(copy) BETWEEN 1 AND 253),
    at TIMESTAMPTZ NOT NULL,
    ready BOOLEAN NOT NULL,
    restarts INTEGER NOT NULL CHECK (restarts >= 0),
    cpu_millicores INTEGER NULL CHECK (cpu_millicores IS NULL OR cpu_millicores >= 0),
    memory_bytes BIGINT NULL CHECK (memory_bytes IS NULL OR memory_bytes >= 0)
);
CREATE INDEX app_metric_samples_app_at ON core.app_metric_samples (app_id, at);
