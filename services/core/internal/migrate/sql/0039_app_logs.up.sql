-- v3-a (GIT-350, H12): log e cronologia delle app. Una riga per riga di log di una copia (pod) e per
-- ogni evento della piattaforma (deploy, riavvio, scala, cambio di stato, OOM). Il testo arriva già
-- mascherato dal masker dei segreti dell'app (appenv.Masker, GIT-348): qui non c'è mai un valore in
-- chiaro. Le righe escono con l'app (ON DELETE CASCADE). Pulizia: per età (7 giorni) e per dimensione
-- (1 GB per app), in store.PruneAppLogs.
CREATE TABLE core.app_log_lines (
    id BIGSERIAL PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('app', 'platform')),
    -- Copia che ha scritto la riga (il nome del pod); NULL per gli eventi della piattaforma.
    copy TEXT NULL CHECK (copy IS NULL OR char_length(copy) BETWEEN 1 AND 253),
    at TIMESTAMPTZ NOT NULL,
    message TEXT NOT NULL CHECK (char_length(message) <= 16384),
    -- Byte della riga: serve alla pulizia per dimensione.
    bytes INTEGER GENERATED ALWAYS AS (octet_length(message)) STORED
);
CREATE INDEX app_log_lines_app_at ON core.app_log_lines (app_id, at, id);
