-- v3-c (GIT-456, H1): interruttore d'istanza delle app pubbliche e storico dei cambi di accesso.
-- L'interruttore sta sulla riga singleton delle impostazioni delle app; spento di default. Il modo
-- `public` scelto per app resta in core.apps.access_mode anche a interruttore spento: l'esposizione
-- la decide il controller, quindi la riaccensione la ripristina.
ALTER TABLE core.app_capacity_settings ADD COLUMN public_apps_enabled BOOLEAN NOT NULL DEFAULT false;

-- Storico: ogni cambio dell'interruttore (app_id NULL) o del modo di accesso di un'app, con chi l'ha
-- fatto (utente o agente, come AppActor). Non e' una versione dell'app.
CREATE TABLE core.app_access_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('public_apps_enabled', 'app_access')),
    app_id UUID NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    old_value TEXT NULL,
    new_value TEXT NOT NULL,
    actor JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK ((kind = 'app_access') = (app_id IS NOT NULL))
);
CREATE INDEX app_access_events_app_idx ON core.app_access_events (app_id, id DESC);
