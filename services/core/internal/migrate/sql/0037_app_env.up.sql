-- v3-a (GIT-348, H8): variabili e segreti delle app. Stanno in tabelle loro, separate da quelle
-- della CI (core.ci_secrets, core.ci_variables, GIT-279): nessun join e nessuna query in comune.
--
-- Variabili: il valore sta qui (leggibile da chi ha write) e ogni modifica crea una versione
-- dell'app che ne copia l'elenco in config.variables. version e' la versione che l'ha impostata.
CREATE TABLE core.app_variables (
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND char_length(name) <= 100),
    value TEXT NOT NULL CHECK (char_length(value) <= 32768),
    version INTEGER NOT NULL CHECK (version >= 1),
    updated_by JSONB NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (app_id, name)
);

-- Segreti: cifrati con lo stesso keyring dei segreti dei webhook e della CI (AES-256-GCM, chiave
-- GITSTACK_WEBHOOK_SECRET_KEY, secret_key_id per la rotazione), con l'id della riga come dato
-- autenticato. Il valore non torna mai da nessuna API e non si rilegge: le versioni dell'app
-- portano solo i nomi (config.secretNames).
CREATE TABLE core.app_secrets (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]*$' AND char_length(name) <= 100),
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    secret_key_id TEXT NOT NULL CHECK (char_length(secret_key_id) BETWEEN 1 AND 64),
    version INTEGER NOT NULL CHECK (version >= 1),
    updated_by JSONB NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT app_secrets_app_name UNIQUE (app_id, name)
);
