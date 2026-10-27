-- v3-a (GIT-403, H12): richieste ed errori 5xx delle app dalle metriche di Traefik. Un campione per
-- app e per raccolta: quante richieste (e quante con codice 5xx) sono arrivate al service dell'app dalla
-- raccolta precedente. Sono differenze di contatori, calcolate dal controller (un reset di Traefik non
-- le rende negative). La prima lettura dopo l'avvio di core scrive un campione a zero: serve a dire che
-- la sorgente risponde. Le righe escono con l'app; la pulizia per eta' e' in store.PruneAppMetrics.
CREATE TABLE core.app_traffic_samples (
    id BIGSERIAL PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    at TIMESTAMPTZ NOT NULL,
    requests BIGINT NOT NULL CHECK (requests >= 0),
    errors_5xx BIGINT NOT NULL CHECK (errors_5xx >= 0 AND errors_5xx <= requests)
);
CREATE INDEX app_traffic_samples_app_at ON core.app_traffic_samples (app_id, at);
