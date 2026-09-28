-- Schema dedicato a core (D6 [c_4df04d65b3ac4910]): uno schema per
-- servizio, nessun servizio legge le tabelle di un altro. In un ambiente di
-- produzione lo schema (e il ruolo DB con permessi solo su di esso) possono
-- essere già stati creati da un operatore (vedi
-- services/core/migrations/bootstrap-role.sql); qui è "IF NOT EXISTS" per
-- restare idempotente anche in sviluppo/CI, dove core si occupa da solo del
-- proprio schema.
CREATE SCHEMA IF NOT EXISTS core;

CREATE TABLE IF NOT EXISTS core.resources (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Dà un significato concreto alla risposta 409 di POST /resources del
    -- contratto: due risorse dello stesso tipo non possono avere lo stesso
    -- nome.
    CONSTRAINT resources_type_name_key UNIQUE (type, name)
);

CREATE INDEX IF NOT EXISTS resources_type_idx ON core.resources (type);
