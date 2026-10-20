-- v2-a (GIT-279, CI4): segreti e variabili della CI, di repo e di organizzazione, e i
-- permessi di default del token del job. I segreti della CI stanno in tabelle loro,
-- separate da quelle dei webhook e dalle future app della v3 (H8).
--
-- Segreto: cifrato con lo stesso keyring dei segreti dei webhook (AES-256-GCM, chiave
-- GITSTACK_WEBHOOK_SECRET_KEY, secret_key_id per la rotazione) con l'id della riga come dato
-- autenticato. Il valore non torna mai da nessuna API.
CREATE TABLE core.ci_secrets (
    id UUID PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repo', 'org')),
    repo_id UUID NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    org_id UUID NULL,
    name TEXT NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$' AND char_length(name) <= 100),
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    secret_key_id TEXT NOT NULL CHECK (char_length(secret_key_id) BETWEEN 1 AND 64),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ci_secrets_scope_target CHECK (
        (scope = 'repo' AND repo_id IS NOT NULL AND org_id IS NULL)
        OR (scope = 'org' AND org_id IS NOT NULL AND repo_id IS NULL))
);
CREATE UNIQUE INDEX ci_secrets_repo_name_key ON core.ci_secrets (repo_id, name) WHERE scope = 'repo';
CREATE UNIQUE INDEX ci_secrets_org_name_key ON core.ci_secrets (org_id, name) WHERE scope = 'org';

CREATE TABLE core.ci_variables (
    id UUID PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repo', 'org')),
    repo_id UUID NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    org_id UUID NULL,
    name TEXT NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$' AND char_length(name) <= 100),
    value TEXT NOT NULL CHECK (char_length(value) <= 65536),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ci_variables_scope_target CHECK (
        (scope = 'repo' AND repo_id IS NOT NULL AND org_id IS NULL)
        OR (scope = 'org' AND org_id IS NOT NULL AND repo_id IS NULL))
);
CREATE UNIQUE INDEX ci_variables_repo_name_key ON core.ci_variables (repo_id, name) WHERE scope = 'repo';
CREATE UNIQUE INDEX ci_variables_org_name_key ON core.ci_variables (org_id, name) WHERE scope = 'org';

-- Permessi di default (= tetto) del token del job di un repo. Mai oltre questi, qualunque
-- cosa chieda `permissions:`; il default e' read.
ALTER TABLE core.ci_repo_state
    ADD COLUMN token_contents TEXT NOT NULL DEFAULT 'read' CHECK (token_contents IN ('none', 'read', 'write')),
    ADD COLUMN token_packages TEXT NOT NULL DEFAULT 'read' CHECK (token_packages IN ('none', 'read', 'write')),
    ADD COLUMN token_actions TEXT NOT NULL DEFAULT 'read' CHECK (token_actions IN ('none', 'read', 'write'));
