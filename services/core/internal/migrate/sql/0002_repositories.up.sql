-- M-03 (GIT-63): un repo e' una riga di core.resources con type='repo' piu'
-- una riga di dettaglio in core.repositories (D-A). Cosi' grant,
-- permissions/check e readable-resources di M-02 funzionano senza modifiche.

-- Il nome di un repo e' unico per owner (vedi repositories sotto), non per
-- tipo: l'unicita' (type, name) resta per tutti gli altri tipi di risorsa.
ALTER TABLE core.resources DROP CONSTRAINT resources_type_name_key;
CREATE UNIQUE INDEX resources_type_name_non_repo_key
    ON core.resources (type, name) WHERE type <> 'repo';

CREATE TABLE core.repositories (
    resource_id UUID PRIMARY KEY REFERENCES core.resources (id) ON DELETE CASCADE,
    -- owner_id e' l'id in identity: nessuna FK fra schemi.
    owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id UUID NOT NULL,
    -- R11: minuscole, cifre, '-', '_', '.', 1-100 caratteri, non inizia
    -- con '.', non finisce con '.git'.
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9_-][a-z0-9._-]{0,99}$' AND name NOT LIKE '%.git'),
    description TEXT NOT NULL DEFAULT '',
    -- P7: privato (default) o interno, nessun accesso anonimo.
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'internal')),
    default_branch TEXT NOT NULL DEFAULT 'main',
    -- R9
    protect_default_branch BOOLEAN NOT NULL DEFAULT true,
    -- R10
    archived_at TIMESTAMPTZ NULL,
    -- R2: eliminazione recuperabile. Il nome resta occupato finche' la riga
    -- non e' cancellata davvero, per questo l'unicita' sotto NON filtra
    -- su deleted_at.
    deleted_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT repositories_owner_name_key UNIQUE (owner_type, owner_id, name)
);

CREATE INDEX repositories_owner_idx ON core.repositories (owner_type, owner_id);
CREATE INDEX repositories_deleted_at_idx ON core.repositories (deleted_at) WHERE deleted_at IS NOT NULL;

-- D-F: modello dati predisposto per le Pull Request (T-07, v1.1). Nessuna API.
-- Il contatore e' per repo e condiviso con le future issues.
CREATE TABLE core.repo_counters (
    repo_id UUID PRIMARY KEY REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    next_number BIGINT NOT NULL DEFAULT 1 CHECK (next_number >= 1)
);

CREATE TABLE core.pull_requests (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    number BIGINT NOT NULL CHECK (number >= 1),
    source_branch TEXT NOT NULL,
    target_branch TEXT NOT NULL,
    title TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed', 'merged')),
    author_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pull_requests_repo_number_key UNIQUE (repo_id, number),
    CONSTRAINT pull_requests_branches_differ CHECK (source_branch <> target_branch)
);
