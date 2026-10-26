-- GIT-376 (H6 del 2026-10-26): lista di accesso dell'app per i tester esterni. Chi e' nella lista
-- apre l'app e nient'altro: la riga NON e' un ruolo sul repo (H5 invariata). Un membro e' un utente
-- (user_id) o un team (team_name = «organizzazione/team», minuscolo, come lo risolve identity).
CREATE TABLE core.app_access_members (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('user', 'team')),
    user_id UUID NULL,
    team_name TEXT NULL CHECK (team_name IS NULL OR char_length(team_name) BETWEEN 3 AND 200),
    added_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK ((kind = 'user' AND user_id IS NOT NULL AND team_name IS NULL)
        OR (kind = 'team' AND team_name IS NOT NULL AND user_id IS NULL))
);
CREATE UNIQUE INDEX app_access_members_user_key ON core.app_access_members (app_id, user_id) WHERE kind = 'user';
CREATE UNIQUE INDEX app_access_members_team_key ON core.app_access_members (app_id, team_name) WHERE kind = 'team';
