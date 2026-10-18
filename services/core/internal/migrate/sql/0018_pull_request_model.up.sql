-- v1.1 (GIT-259): modello dati delle Pull Request sopra core.pull_requests
-- (0002). Regole PR1-PR12 in .prisma/knowledge/topics/pull-request.md.
-- Nessuna FK verso identity (gli utenti vivono in un altro schema): gli
-- *_id utente sono UUID senza vincolo, come in core.issues. Il numero #n
-- resta quello del contatore condiviso con le issues (I1): il trigger
-- pull_requests_number_not_taken di 0004 continua a valere.
-- Tutto sta nello schema core: pg_dump --schema=core (backup/restore/upgrade)
-- include le tabelle nuove senza modifiche all'admin.

-- Stato della PR: bozza, chiusura, unione (PR1, PR6).
ALTER TABLE core.pull_requests
    ADD COLUMN body TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 65536),
    ADD COLUMN draft BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN base_sha TEXT NULL,
    ADD COLUMN head_sha TEXT NULL,
    ADD COLUMN merge_method TEXT NULL CHECK (merge_method IN ('merge', 'squash')),
    ADD COLUMN merge_commit_sha TEXT NULL,
    ADD COLUMN merged_by UUID NULL,
    ADD COLUMN merged_at TIMESTAMPTZ NULL,
    ADD COLUMN closed_at TIMESTAMPTZ NULL,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- I dettagli del merge esistono solo per le PR unite. Le PR unite della
-- v1.0 non li hanno (colonne NULL): il vincolo non lo impone al contrario.
ALTER TABLE core.pull_requests
    ADD CONSTRAINT pull_requests_merge_fields_only_if_merged CHECK (
        state = 'merged'
        OR (merge_method IS NULL AND merge_commit_sha IS NULL AND merged_by IS NULL AND merged_at IS NULL)),
    ADD CONSTRAINT pull_requests_merged_not_draft CHECK (NOT (draft AND state = 'merged'));
CREATE INDEX pull_requests_repo_state_idx ON core.pull_requests (repo_id, state, number DESC);

-- PR2: review. by_agent=true: la review di un agente non conta mai come
-- approvazione valida (la scrittura la accetta, il conteggio la ignora).
CREATE TABLE core.pull_request_reviews (
    id UUID PRIMARY KEY,
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('comment', 'approve', 'request_changes')),
    body TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 65536),
    commit_sha TEXT NOT NULL,
    by_agent BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_request_reviews_pr_idx ON core.pull_request_reviews (pull_request_id, created_at);
CREATE INDEX pull_request_reviews_author_idx ON core.pull_request_reviews (pull_request_id, author_id, created_at DESC);

-- PR5: conversazioni sulle righe. Una conversazione e' ancorata a commit,
-- file e righe; outdated quando il codice cambia; risolta da chi la chiude.
CREATE TABLE core.pull_request_threads (
    id UUID PRIMARY KEY,
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    review_id UUID NULL REFERENCES core.pull_request_reviews (id) ON DELETE SET NULL,
    commit_sha TEXT NOT NULL,
    path TEXT NOT NULL CHECK (char_length(path) BETWEEN 1 AND 4096),
    side TEXT NOT NULL DEFAULT 'right' CHECK (side IN ('left', 'right')),
    start_line INTEGER NULL CHECK (start_line >= 1),
    line INTEGER NOT NULL CHECK (line >= 1),
    outdated BOOLEAN NOT NULL DEFAULT false,
    resolved BOOLEAN NOT NULL DEFAULT false,
    resolved_by UUID NULL,
    resolved_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pull_request_threads_range CHECK (start_line IS NULL OR start_line <= line),
    CONSTRAINT pull_request_threads_resolved_fields CHECK (
        resolved = (resolved_at IS NOT NULL) AND (resolved OR resolved_by IS NULL))
);
CREATE INDEX pull_request_threads_pr_idx ON core.pull_request_threads (pull_request_id, path, line);

CREATE TABLE core.pull_request_comments (
    id UUID PRIMARY KEY,
    thread_id UUID NOT NULL REFERENCES core.pull_request_threads (id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 65536),
    by_agent BOOLEAN NOT NULL DEFAULT false,
    edited BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_request_comments_thread_idx ON core.pull_request_comments (thread_id, created_at);

-- PR12c: revisori richiesti. Chi ha almeno `read` sul repo (controllo in
-- applicazione); una richiesta per utente, rinnovabile.
CREATE TABLE core.pull_request_review_requests (
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    requested_by UUID NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, reviewer_id)
);

-- PR9: stati dei commit da sistemi esterni. Uno stato per (repo, sha,
-- contesto): chi scrive di nuovo aggiorna la riga all'ultimo valore
--   INSERT ... ON CONFLICT (repo_id, sha, context) DO UPDATE SET ...
CREATE TABLE core.commit_statuses (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    sha TEXT NOT NULL CHECK (sha ~ '^[0-9a-f]{40}([0-9a-f]{24})?$'),
    context TEXT NOT NULL CHECK (char_length(context) BETWEEN 1 AND 255),
    state TEXT NOT NULL CHECK (state IN ('pending', 'success', 'failure', 'error')),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 1024),
    target_url TEXT NOT NULL DEFAULT '' CHECK (char_length(target_url) <= 2048),
    creator_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT commit_statuses_repo_sha_context_key UNIQUE (repo_id, sha, context)
);

-- PR3, PR4: impostazioni del branch principale (una riga per repo; senza
-- riga valgono i default: nessuna PR obbligatoria, entrambi i metodi, il
-- branch non si elimina da solo).
CREATE TABLE core.branch_settings (
    repo_id UUID PRIMARY KEY REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    require_pull_request BOOLEAN NOT NULL DEFAULT false,
    required_approvals SMALLINT NOT NULL DEFAULT 1 CHECK (required_approvals BETWEEN 1 AND 5),
    required_contexts TEXT[] NOT NULL DEFAULT '{}',
    merge_methods TEXT[] NOT NULL DEFAULT ARRAY['merge', 'squash'],
    delete_branch_on_merge BOOLEAN NOT NULL DEFAULT false,
    updated_by UUID NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT branch_settings_merge_methods CHECK (
        cardinality(merge_methods) BETWEEN 1 AND 2
        AND merge_methods <@ ARRAY['merge', 'squash']
        AND (cardinality(merge_methods) = 1 OR merge_methods[1] <> merge_methods[2]))
);

-- PR12b: file Viewed, personali. viewed_sha e' lo sha dell'head al momento
-- della spunta: se l'head cambia il file si riapre (confronto in
-- applicazione).
CREATE TABLE core.pull_request_viewed_files (
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    path TEXT NOT NULL CHECK (char_length(path) BETWEEN 1 AND 4096),
    viewed_sha TEXT NOT NULL,
    viewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, user_id, path)
);
