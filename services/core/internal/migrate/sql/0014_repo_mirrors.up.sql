-- M-04/V8 (GIT-179): mirror in push. Un repo puo' avere mirror verso un remote
-- HTTPS (es. GitHub): a ogni push del branch principale o di un tag, core
-- chiede al servizio git di spingerli, MAI con un push forzato. Gestiti da chi
-- ha admin sul repo.
--
-- Il token e' cifrato come i segreti dei webhook (AES-256-GCM, 0006): ciphertext,
-- nonce e id della chiave; l'id del mirror e' il dato autenticato. Non torna
-- mai nelle risposte dell'API.
--
-- La riga e' anche la coda: `pending` + `next_attempt_at` dicono che c'e' un
-- push da fare; `lease_until` affitta il mirror a un solo worker alla volta
-- (FOR UPDATE SKIP LOCKED), anche con piu' repliche.
--   pending   in coda (mai spinto, o un push nuovo da fare)
--   syncing   un worker lo sta spingendo (con affitto)
--   in_sync   la destinazione ha lo stato dell'ultimo push riuscito
--   error     ultimo tentativo fallito, si ritenta con attesa crescente
--   diverged  la destinazione ha una storia diversa: fermo, nessun
--             tentativo automatico finche' un admin non usa «Sincronizza ora»
CREATE TABLE core.repo_mirrors (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    url TEXT NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2048),
    username TEXT NOT NULL CHECK (char_length(username) BETWEEN 1 AND 256),
    token_ciphertext BYTEA NOT NULL,
    token_nonce BYTEA NOT NULL,
    token_key_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'syncing', 'in_sync', 'error', 'diverged')),
    pending BOOLEAN NOT NULL DEFAULT true,
    -- Contatore delle richieste di push (evento, «Sincronizza ora»): un worker
    -- che finisce chiude solo quelle che aveva visto, un push arrivato nel
    -- frattempo resta in coda.
    push_seq BIGINT NOT NULL DEFAULT 1,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ NULL,
    last_attempt_at TIMESTAMPTZ NULL,
    last_success_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 1024),
    -- ref → sha spinti con l'ultimo push riuscito.
    last_pushed JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repo_id, url)
);
CREATE INDEX repo_mirrors_repo_idx ON core.repo_mirrors (repo_id, created_at, id);
-- Mirror da spingere, in ordine di scadenza.
CREATE INDEX repo_mirrors_due_idx ON core.repo_mirrors (next_attempt_at) WHERE enabled AND pending;

-- Log delle ultime esecuzioni di ogni mirror (potato: le ultime 50 e 30 giorni).
CREATE TABLE core.repo_mirror_runs (
    id UUID PRIMARY KEY,
    mirror_id UUID NOT NULL REFERENCES core.repo_mirrors (id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    -- success | error | diverged | blocked (destinazione non ammessa da C8).
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'error', 'diverged', 'blocked')),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    error TEXT NOT NULL DEFAULT '' CHECK (char_length(error) <= 1024),
    -- ref → sha spinti o gia' allineati, e l'esito per ref.
    pushed JSONB NOT NULL DEFAULT '{}'::jsonb,
    refs JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX repo_mirror_runs_mirror_idx ON core.repo_mirror_runs (mirror_id, started_at DESC, id);
CREATE INDEX repo_mirror_runs_started_idx ON core.repo_mirror_runs (started_at);

-- Notifica agli admin quando un mirror diverge: nuovo motivo `mirror`, legato
-- al mirror (come `webhook` lo e' al webhook).
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror'));
ALTER TABLE core.notification_preferences DROP CONSTRAINT notification_preferences_reason_check;
ALTER TABLE core.notification_preferences ADD CONSTRAINT notification_preferences_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror'));
ALTER TABLE core.notifications ADD COLUMN mirror_id UUID NULL REFERENCES core.repo_mirrors (id) ON DELETE CASCADE;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_mirror_matches_reason CHECK ((reason = 'mirror') = (mirror_id IS NOT NULL));
