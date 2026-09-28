-- Lo schema dedicato "core" (D6 [c_4df04d65b3ac4910]) non lo crea questa
-- migrazione: ci pensa internal/migrate.EnsureSchema, eseguita prima di
-- aprire questa connessione di migrazione (la tabella di stato di
-- golang-migrate, core.schema_migrations, ha già bisogno dello schema
-- prima della 0001). EnsureSchema controlla pg_namespace ed esegue
-- CREATE SCHEMA solo se manca davvero: con un ruolo a permessi limitati che
-- possiede già lo schema (bootstrap-role.sql) senza CREATE sul database,
-- un "CREATE SCHEMA IF NOT EXISTS" qui fallirebbe comunque con
-- "permission denied for database" (Postgres controlla il privilegio
-- CREATE prima di valutare IF NOT EXISTS).
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
