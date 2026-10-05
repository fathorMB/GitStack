-- M-05/A (GIT-101): issues, commenti, versioni dei testi, cronologia,
-- etichette, milestone, assegnatari e metadati degli allegati.
-- Regole I1-I11 in .prisma/knowledge/topics/issues.md. Nessuna FK verso
-- identity (gli utenti vivono in un altro schema): gli *_id utente sono UUID
-- senza vincolo, come owner_id in core.repositories.

-- I1: un solo contatore per repo, condiviso da issues e PR. Le milestone
-- hanno un numero per repo indipendente (I7), con il proprio contatore.
ALTER TABLE core.repo_counters ADD COLUMN next_milestone_number BIGINT NOT NULL DEFAULT 1
    CHECK (next_milestone_number >= 1);

-- La creazione del repo crea sempre la riga del contatore; per i repo gia'
-- esistenti la migrazione la crea se manca.
INSERT INTO core.repo_counters (repo_id)
    SELECT resource_id FROM core.repositories
    ON CONFLICT (repo_id) DO NOTHING;
-- Se ci fossero gia' PR numerate, il contatore parte oltre il loro massimo.
UPDATE core.repo_counters c
   SET next_number = GREATEST(c.next_number, m.max_number + 1)
  FROM (SELECT repo_id, max(number) AS max_number FROM core.pull_requests GROUP BY repo_id) m
 WHERE m.repo_id = c.repo_id;

CREATE TABLE core.milestones (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    number BIGINT NOT NULL CHECK (number >= 1),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 256),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 4096),
    due_on DATE NULL,
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    closed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT milestones_repo_number_key UNIQUE (repo_id, number),
    CONSTRAINT milestones_closed_at_matches_state CHECK ((state = 'closed') = (closed_at IS NOT NULL))
);
-- Il titolo e' unico nel repo senza distinguere maiuscole/minuscole.
CREATE UNIQUE INDEX milestones_repo_title_key ON core.milestones (repo_id, lower(title));

CREATE TABLE core.labels (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50 AND name !~ '[/,[:cntrl:]]'),
    color TEXT NOT NULL CHECK (color ~ '^[0-9a-fA-F]{6}$'),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 256),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX labels_repo_name_key ON core.labels (repo_id, lower(name));

-- I5: etichette predefinite, da chiamare alla creazione del repo (casella
-- attiva di default). Idempotente. Non e' un trigger: l'opzione puo' essere
-- spenta, e un repo esistente non le riceve da solo.
CREATE OR REPLACE FUNCTION core.seed_default_labels(p_repo_id UUID) RETURNS VOID
LANGUAGE sql AS $$
    INSERT INTO core.labels (id, repo_id, name, color, description)
    SELECT gen_random_uuid(), p_repo_id, d.name, d.color, d.description
      FROM (VALUES
        ('bug',               'd73a4a', 'Qualcosa non funziona'),
        ('enhancement',       'a2eeef', 'Nuova funzione o miglioria'),
        ('documentation',     '0075ca', 'Documentazione da scrivere o correggere'),
        ('question',          'd876e3', 'Serve una risposta'),
        ('duplicate',         'cfd3d7', 'Esiste già'),
        ('good first issue',  '7057ff', 'Adatta a chi inizia'),
        ('agent-ready',       '0e8a16', 'Descritta abbastanza da poter essere presa da un agente'),
        ('needs-human',       'fbca04', 'Serve una decisione o un intervento umano')
      ) AS d(name, color, description)
    ON CONFLICT (repo_id, lower(name)) DO NOTHING
$$;

-- I1, I2, I4, I11.
CREATE TABLE core.issues (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    -- Preso da core.repo_counters nella stessa transazione dell'INSERT:
    --   UPDATE core.repo_counters SET next_number = next_number + 1
    --    WHERE repo_id = $1 RETURNING next_number - 1
    -- Mai riusato: le issues non si eliminano (I4) e il contatore non scende.
    number BIGINT NOT NULL CHECK (number >= 1),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 256),
    body TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 65536),
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    -- I2: il motivo c'e' se e solo se e' chiusa; riaprire lo azzera.
    close_reason TEXT NULL CHECK (close_reason IN ('completed', 'not_planned', 'duplicate')),
    duplicate_of BIGINT NULL,
    author_id UUID NOT NULL,
    milestone_id UUID NULL REFERENCES core.milestones (id) ON DELETE SET NULL,
    locked BOOLEAN NOT NULL DEFAULT false,
    lock_reason TEXT NOT NULL DEFAULT '',
    hidden BOOLEAN NOT NULL DEFAULT false,
    edited BOOLEAN NOT NULL DEFAULT false,
    closed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- I10: ricerca testuale PostgreSQL; configurazione 'simple' perche' i
    -- testi sono misti italiano/inglese e i nomi tecnici non vanno
    -- stemmati.
    search tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', title || ' ' || body)
    ) STORED,
    CONSTRAINT issues_repo_number_key UNIQUE (repo_id, number),
    CONSTRAINT issues_closed_has_reason CHECK (
        (state = 'open' AND close_reason IS NULL AND closed_at IS NULL)
        OR (state = 'closed' AND close_reason IS NOT NULL AND closed_at IS NOT NULL)),
    CONSTRAINT issues_duplicate_of_matches_reason CHECK (
        (close_reason = 'duplicate') = (duplicate_of IS NOT NULL)),
    CONSTRAINT issues_duplicate_of_other CHECK (duplicate_of IS NULL OR duplicate_of <> number),
    -- Il duplicato e' un'altra issue dello stesso repo.
    CONSTRAINT issues_duplicate_of_fkey FOREIGN KEY (repo_id, duplicate_of)
        REFERENCES core.issues (repo_id, number)
);
CREATE INDEX issues_repo_state_idx ON core.issues (repo_id, state, number DESC);
CREATE INDEX issues_repo_author_idx ON core.issues (repo_id, author_id);
CREATE INDEX issues_milestone_idx ON core.issues (milestone_id) WHERE milestone_id IS NOT NULL;
CREATE INDEX issues_search_idx ON core.issues USING GIN (search);

CREATE TABLE core.issue_comments (
    id UUID PRIMARY KEY,
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    body TEXT NOT NULL CHECK (char_length(body) <= 65536),
    edited BOOLEAN NOT NULL DEFAULT false,
    -- I4: un commento eliminato resta come traccia, senza testo.
    deleted_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    search tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED
);
CREATE INDEX issue_comments_issue_idx ON core.issue_comments (issue_id, created_at, id);
CREATE INDEX issue_comments_search_idx ON core.issue_comments USING GIN (search);

-- I4: versioni precedenti del testo, per admin. La corrente sta nella issue
-- o nel commento; qui entra quella che una modifica sostituisce.
CREATE TABLE core.issue_text_versions (
    id UUID PRIMARY KEY,
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    comment_id UUID NULL REFERENCES core.issue_comments (id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version >= 1),
    title TEXT NULL,
    body TEXT NOT NULL,
    editor_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_text_versions_issue_key ON core.issue_text_versions (issue_id, version)
    WHERE comment_id IS NULL;
CREATE UNIQUE INDEX issue_text_versions_comment_key ON core.issue_text_versions (comment_id, version)
    WHERE comment_id IS NOT NULL;

-- Cronologia. `data` ha i dettagli per tipo (vedi lo schema IssueEvent).
CREATE TABLE core.issue_events (
    id UUID PRIMARY KEY,
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN (
        'opened', 'closed', 'reopened', 'renamed', 'edited', 'labeled', 'unlabeled',
        'assigned', 'unassigned', 'milestoned', 'demilestoned', 'locked', 'unlocked',
        'hidden', 'unhidden', 'comment_deleted', 'referenced')),
    -- NULL per gli eventi di sistema (es. chiusura da `fixes #n`).
    actor_id UUID NULL,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX issue_events_issue_idx ON core.issue_events (issue_id, created_at, id);

CREATE TABLE core.issue_labels (
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES core.labels (id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX issue_labels_label_idx ON core.issue_labels (label_id);

-- I6: persone o agenti, fino a 10 per issue (trigger sotto). Che abbiano
-- `write` lo verifica l'applicazione con identity.
CREATE TABLE core.issue_assignees (
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    assigned_by UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX issue_assignees_user_idx ON core.issue_assignees (user_id);

-- I9: solo metadati. Il file sta sul volume degli allegati, in
-- <repo_id>/<id> senza il nome originale (README di core). Nasce non
-- collegato (issue_id NULL) e viene collegato alla creazione della issue o
-- del commento; i non collegati si possono eliminare dopo 24 ore.
CREATE TABLE core.issue_attachments (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    issue_id UUID NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    comment_id UUID NULL REFERENCES core.issue_comments (id) ON DELETE CASCADE,
    uploader_id UUID NOT NULL,
    filename TEXT NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255),
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT issue_attachments_comment_needs_issue CHECK (comment_id IS NULL OR issue_id IS NOT NULL)
);
CREATE INDEX issue_attachments_issue_idx ON core.issue_attachments (issue_id) WHERE issue_id IS NOT NULL;
CREATE INDEX issue_attachments_unlinked_idx ON core.issue_attachments (created_at) WHERE issue_id IS NULL;

-- Controlli di integrita' che un vincolo dichiarativo non sa esprimere.
--
-- 1) Numerazione condivisa (I1). Issues e PR prendono il numero dallo stesso
--    contatore, quindi non possono collidere: due transazioni concorrenti
--    ricevono numeri diversi perche' l'UPDATE ... RETURNING serializza sulla
--    riga del contatore. UNIQUE(repo_id, number) c'e' su ciascuna tabella, ma
--    non vede l'altra: questo trigger rifiuta (23505) un numero gia' occupato
--    dall'altra tabella, cioe' un INSERT che non e' passato dal contatore.
--    Costa una ricerca per indice sull'altra tabella.
CREATE OR REPLACE FUNCTION core.check_number_not_taken() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'issues' THEN
        IF EXISTS (SELECT 1 FROM core.pull_requests WHERE repo_id = NEW.repo_id AND number = NEW.number) THEN
            RAISE EXCEPTION 'numero % del repo % gia'' usato da una pull request', NEW.number, NEW.repo_id
                USING ERRCODE = '23505', CONSTRAINT = 'issues_number_collides_with_pull_requests';
        END IF;
    ELSE
        IF EXISTS (SELECT 1 FROM core.issues WHERE repo_id = NEW.repo_id AND number = NEW.number) THEN
            RAISE EXCEPTION 'numero % del repo % gia'' usato da una issue', NEW.number, NEW.repo_id
                USING ERRCODE = '23505', CONSTRAINT = 'pull_requests_number_collides_with_issues';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER issues_number_not_taken BEFORE INSERT ON core.issues
    FOR EACH ROW EXECUTE FUNCTION core.check_number_not_taken();
CREATE TRIGGER pull_requests_number_not_taken BEFORE INSERT ON core.pull_requests
    FOR EACH ROW EXECUTE FUNCTION core.check_number_not_taken();

-- 2) Al massimo 10 assegnatari (I6). Blocca la riga della issue per
--    serializzare gli inserimenti concorrenti, poi conta.
CREATE OR REPLACE FUNCTION core.check_assignee_limit() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    n INTEGER;
BEGIN
    PERFORM 1 FROM core.issues WHERE id = NEW.issue_id FOR UPDATE;
    SELECT count(*) INTO n FROM core.issue_assignees WHERE issue_id = NEW.issue_id;
    IF n >= 10 THEN
        RAISE EXCEPTION 'la issue % ha gia'' 10 assegnatari', NEW.issue_id
            USING ERRCODE = '23514', CONSTRAINT = 'issue_assignees_max_10';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER issue_assignees_max BEFORE INSERT ON core.issue_assignees
    FOR EACH ROW EXECUTE FUNCTION core.check_assignee_limit();
