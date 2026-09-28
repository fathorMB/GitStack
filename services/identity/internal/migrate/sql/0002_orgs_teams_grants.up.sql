-- Organizzazioni, team, membership e grant su risorse generiche (D15).

CREATE TABLE identity.organizations (
    id           UUID PRIMARY KEY,
    name         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organizations_name_format CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$')
);
CREATE UNIQUE INDEX organizations_name_key ON identity.organizations (name);

CREATE TABLE identity.org_members (
    org_id     UUID NOT NULL REFERENCES identity.organizations (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id),
    CONSTRAINT org_members_role_check CHECK (role IN ('owner', 'member'))
);
CREATE INDEX org_members_user_id_idx ON identity.org_members (user_id);

CREATE TABLE identity.teams (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES identity.organizations (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT teams_name_format CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$'),
    -- Serve come bersaglio della FK composta di team_members.
    CONSTRAINT teams_id_org_key UNIQUE (id, org_id)
);
CREATE UNIQUE INDEX teams_org_name_key ON identity.teams (org_id, name);

-- Un membro di team deve essere anche membro dell'organizzazione: la FK
-- composta verso org_members lo impone nel database; rimuovere l'utente
-- dall'organizzazione lo toglie a cascata da tutti i suoi team.
CREATE TABLE identity.team_members (
    team_id    UUID NOT NULL,
    org_id     UUID NOT NULL,
    user_id    UUID NOT NULL,
    role       TEXT NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id),
    CONSTRAINT team_members_team_fk FOREIGN KEY (team_id, org_id)
        REFERENCES identity.teams (id, org_id) ON DELETE CASCADE,
    CONSTRAINT team_members_org_member_fk FOREIGN KEY (org_id, user_id)
        REFERENCES identity.org_members (org_id, user_id) ON DELETE CASCADE,
    CONSTRAINT team_members_role_check CHECK (role IN ('member', 'maintainer'))
);
CREATE INDEX team_members_user_id_idx ON identity.team_members (user_id);
CREATE INDEX team_members_org_user_idx ON identity.team_members (org_id, user_id);

-- Grant su risorse generiche: resource_id punta a core.resources ma senza FK
-- (D6: nessuna dipendenza fra schemi di servizi diversi). Il soggetto e' un
-- utente oppure un team, mai entrambi (CHECK).
CREATE TABLE identity.resource_grants (
    id          UUID PRIMARY KEY,
    resource_id UUID NOT NULL,
    user_id     UUID REFERENCES identity.users (id) ON DELETE CASCADE,
    team_id     UUID REFERENCES identity.teams (id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    granted_by  UUID REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT resource_grants_one_subject CHECK (num_nonnulls(user_id, team_id) = 1),
    CONSTRAINT resource_grants_role_check CHECK (role IN ('read', 'write', 'admin'))
);
CREATE UNIQUE INDEX resource_grants_user_key ON identity.resource_grants (resource_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX resource_grants_team_key ON identity.resource_grants (resource_id, team_id) WHERE team_id IS NOT NULL;
CREATE INDEX resource_grants_user_idx ON identity.resource_grants (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX resource_grants_team_idx ON identity.resource_grants (team_id) WHERE team_id IS NOT NULL;
