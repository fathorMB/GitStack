-- M-09/A1 (GIT-195): mirror in pull (MP1-MP8). GitStack scarica periodicamente
-- un repo Git esterno in HTTPS e ne tiene una copia prudente: tutti i branch e
-- i tag, niente cancellazioni, un ref riscritto o cancellato nell'origine resta
-- com'e' ed e' segnato `diverged` (MP1). Gemello del mirror in push (0014):
-- stessi nomi di stato e stessa cifratura del token.
--
-- Un repo ha al massimo un mirror in pull (UNIQUE su repo_id). L'interruzione
-- (MP6) non cancella la riga: la porta a `stopped`, toglie le credenziali e
-- conserva registro e ref `diverged`; riattivare (MP3) riusa la riga.
--
-- Il token e' facoltativo (MP3), cifrato come quello dei mirror in push e dei
-- webhook (AES-256-GCM, 0006): ciphertext, nonce e id della chiave, con l'id del
-- mirror come dato autenticato. Non torna mai nelle risposte dell'API.
--
-- La riga e' anche la coda: `next_attempt_at` dice quando e' il prossimo
-- aggiornamento, `lease_until` affitta il mirror a un solo worker alla volta
-- (FOR UPDATE SKIP LOCKED, «un aggiornamento alla volta», MP4).
--   pending   in coda (primo aggiornamento, prossimo giro, «Sincronizza ora»)
--   syncing   un worker lo sta aggiornando (con affitto)
--   in_sync   ultimo aggiornamento riuscito (anche con ref `diverged`)
--   error     ultimo tentativo fallito, si ritenta con attesa crescente
--             (30 s, 2 min, 10 min, 1 h, 3 h, poi ogni 6 h)
--   paused    fermato da un admin: nessun aggiornamento, repo in sola lettura
--   stopped   interrotto: repo normale e scrivibile, niente credenziali
CREATE TABLE core.repo_pull_mirrors (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL UNIQUE REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    origin_url TEXT NOT NULL CHECK (char_length(origin_url) BETWEEN 1 AND 2048),
    -- mirror: «mantieni sincronizzato»; once: «copia una volta sola» (MP8), a
    -- copia finita il mirror passa da solo a `stopped`.
    mode TEXT NOT NULL DEFAULT 'mirror' CHECK (mode IN ('mirror', 'once')),
    interval_seconds INTEGER NOT NULL DEFAULT 600 CHECK (interval_seconds BETWEEN 300 AND 86400),
    username TEXT NULL CHECK (username IS NULL OR char_length(username) BETWEEN 1 AND 256),
    token_ciphertext BYTEA NULL,
    token_nonce BYTEA NULL,
    token_key_id TEXT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'syncing', 'in_sync', 'error', 'paused', 'stopped')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NULL DEFAULT now(),
    lease_until TIMESTAMPTZ NULL,
    last_attempt_at TIMESTAMPTZ NULL,
    last_success_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 1024),
    -- Quando e' partito l'avviso «nessun aggiornamento da piu' di 24 ore»
    -- (MP4): un solo avviso per periodo di errore.
    stale_notified_at TIMESTAMPTZ NULL,
    stopped_at TIMESTAMPTZ NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Credenziali tutte o niente.
    CONSTRAINT repo_pull_mirrors_token_all_or_none CHECK (
        (token_ciphertext IS NULL AND token_nonce IS NULL AND token_key_id IS NULL)
        OR (token_ciphertext IS NOT NULL AND token_nonce IS NOT NULL AND token_key_id IS NOT NULL)),
    -- Interrotto: niente credenziali e niente coda.
    CONSTRAINT repo_pull_mirrors_stopped_clean CHECK (
        (state = 'stopped') = (stopped_at IS NOT NULL)),
    CONSTRAINT repo_pull_mirrors_stopped_no_token CHECK (state <> 'stopped' OR token_ciphertext IS NULL)
);
-- Mirror da aggiornare, in ordine di scadenza.
CREATE INDEX repo_pull_mirrors_due_idx ON core.repo_pull_mirrors (next_attempt_at)
    WHERE state IN ('pending', 'error', 'in_sync') AND next_attempt_at IS NOT NULL;

-- Registro delle ultime esecuzioni (potato: le ultime 50 e 30 giorni, MP4).
CREATE TABLE core.repo_pull_mirror_runs (
    id UUID PRIMARY KEY,
    mirror_id UUID NOT NULL REFERENCES core.repo_pull_mirrors (id) ON DELETE CASCADE,
    -- initial: prima copia; schedule: a intervallo; manual: «Sincronizza ora» o Riprendi.
    trigger TEXT NOT NULL DEFAULT 'schedule' CHECK (trigger IN ('initial', 'schedule', 'manual')),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    -- success | diverged (riuscito ma con ref divergenti) | error | blocked
    -- (origine non ammessa dalla policy di uscita C8, non si ritenta).
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'diverged', 'error', 'blocked')),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    error TEXT NOT NULL DEFAULT '' CHECK (char_length(error) <= 1024),
    -- Esito per ref cambiato o divergente: [{ref, status, oldSha, newSha, reason}].
    refs JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX repo_pull_mirror_runs_mirror_idx ON core.repo_pull_mirror_runs (mirror_id, started_at DESC, id);
CREATE INDEX repo_pull_mirror_runs_started_idx ON core.repo_pull_mirror_runs (started_at);

-- Ref divergenti (MP1): uno per ref, finche' l'origine non torna a essere un
-- fast-forward della versione conservata. Restano dopo l'interruzione (MP6).
CREATE TABLE core.repo_pull_mirror_refs (
    mirror_id UUID NOT NULL REFERENCES core.repo_pull_mirrors (id) ON DELETE CASCADE,
    ref TEXT NOT NULL CHECK (char_length(ref) BETWEEN 1 AND 1024),
    -- rewritten: riscritto nell'origine; deleted: cancellato nell'origine.
    kind TEXT NOT NULL CHECK (kind IN ('rewritten', 'deleted')),
    local_sha TEXT NOT NULL CHECK (char_length(local_sha) BETWEEN 4 AND 64),
    -- NULL se il ref e' stato cancellato.
    remote_sha TEXT NULL CHECK (remote_sha IS NULL OR char_length(remote_sha) BETWEEN 4 AND 64),
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (mirror_id, ref),
    CONSTRAINT repo_pull_mirror_refs_deleted_no_remote CHECK ((kind = 'deleted') = (remote_sha IS NULL))
);
