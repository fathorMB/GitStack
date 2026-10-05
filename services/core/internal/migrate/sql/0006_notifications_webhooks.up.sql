-- M-06/A (GIT-129): notifiche, iscrizioni, watch, preferenze email, webhook,
-- consegne e riferimenti issue <-> commit / issue <-> issue.
-- Regole C1-C9 in .prisma/knowledge/topics/collegamenti-notifiche-webhook.md.
-- Come in 0004 nessuna FK verso identity: gli *_id utente e organizzazione
-- sono UUID senza vincolo.

-- C1, C2: la cronologia della issue ha tre tipi nuovi.
--   referenced_from  un'altra issue/PR (o un suo commento) cita questa issue
--   commit_linked    un commit cita questa issue (anche su un branch non principale)
--   closed_by_commit chiusura "completata" da `fixes #n` quando il commit
--                    entra nel branch principale (data.commit)
-- `referenced` (0004) resta ammesso ma non e' piu' scritto: lo sostituisce
-- commit_linked.
ALTER TABLE core.issue_events DROP CONSTRAINT issue_events_type_check;
ALTER TABLE core.issue_events ADD CONSTRAINT issue_events_type_check CHECK (type IN (
    'opened', 'closed', 'reopened', 'renamed', 'edited', 'labeled', 'unlabeled',
    'assigned', 'unassigned', 'milestoned', 'demilestoned', 'locked', 'unlocked',
    'hidden', 'unhidden', 'comment_deleted', 'referenced',
    'referenced_from', 'commit_linked', 'closed_by_commit'));

-- C3: iscrizione a una issue. Una riga con subscribed=false e' un
-- "Unsubscribe" esplicito: l'iscrizione automatica (autore, assegnatari,
-- commentatori, menzionati) non la sovrascrive piu'.
CREATE TABLE core.issue_subscriptions (
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    subscribed BOOLEAN NOT NULL DEFAULT true,
    reason TEXT NOT NULL CHECK (reason IN ('author', 'assignee', 'commenter', 'mentioned', 'manual')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX issue_subscriptions_user_idx ON core.issue_subscriptions (user_id);

-- C3: Watch del repo. Nessuna riga = 'participating' (default).
CREATE TABLE core.repo_watches (
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('participating', 'all', 'ignore')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, user_id)
);
CREATE INDEX repo_watches_user_idx ON core.repo_watches (user_id);
-- Chi segue "tutto" su un repo (consumer delle notifiche).
CREATE INDEX repo_watches_repo_mode_idx ON core.repo_watches (repo_id, mode);

-- C5: preferenze email per tipo (= motivo della notifica). Nessuna riga =
-- default (email per mentioned e assigned, non per gli altri tipi).
CREATE TABLE core.notification_preferences (
    user_id UUID NOT NULL,
    reason TEXT NOT NULL CHECK (reason IN (
        'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook')),
    email_enabled BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, reason)
);

-- C6, C7: webhook di repo (scope 'repo', admin del repo) o di
-- organizzazione (scope 'org', owner). org_id e' l'id dell'organizzazione in
-- identity (nessuna FK).
--
-- Segreto: cifrato, non hash, perche' X-GitStack-Signature e' un HMAC-SHA256
-- calcolato a ogni consegna e serve il segreto in chiaro. AES-256-GCM con la
-- chiave GITSTACK_WEBHOOK_SECRET_KEY; secret_key_id dice con quale chiave e'
-- cifrato (rotazione). Tutte e tre NULL = webhook senza segreto (consegna non
-- firmata). Il segreto non torna mai nelle risposte (README di core).
CREATE TABLE core.webhooks (
    id UUID PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repo', 'org')),
    repo_id UUID NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    org_id UUID NULL,
    url TEXT NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2048 AND url ~ '^https?://'),
    events TEXT[] NOT NULL CHECK (
        cardinality(events) >= 1
        AND events <@ ARRAY['push', 'issues', 'issue_comment', 'repository']::TEXT[]),
    active BOOLEAN NOT NULL DEFAULT true,
    secret_ciphertext BYTEA NULL,
    secret_nonce BYTEA NULL CHECK (secret_nonce IS NULL OR octet_length(secret_nonce) = 12),
    secret_key_id TEXT NULL CHECK (secret_key_id IS NULL OR char_length(secret_key_id) BETWEEN 1 AND 64),
    -- C7: inizio dei fallimenti consecutivi (azzerato da una consegna
    -- riuscita); dopo 3 giorni il webhook si disattiva.
    failing_since TIMESTAMPTZ NULL,
    -- Disattivazione automatica (C7): quando e perche'. Il PATCH active=false
    -- di una persona non li imposta; la riattivazione li azzera.
    disabled_at TIMESTAMPTZ NULL,
    disabled_reason TEXT NULL CHECK (disabled_reason IN ('consecutive_failures')),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT webhooks_scope_target CHECK (
        (scope = 'repo' AND repo_id IS NOT NULL AND org_id IS NULL)
        OR (scope = 'org' AND org_id IS NOT NULL AND repo_id IS NULL)),
    CONSTRAINT webhooks_secret_all_or_none CHECK (
        (secret_ciphertext IS NULL AND secret_nonce IS NULL AND secret_key_id IS NULL)
        OR (secret_ciphertext IS NOT NULL AND secret_nonce IS NOT NULL AND secret_key_id IS NOT NULL)),
    CONSTRAINT webhooks_disabled_matches_reason CHECK ((disabled_at IS NULL) = (disabled_reason IS NULL))
);
CREATE INDEX webhooks_repo_idx ON core.webhooks (repo_id) WHERE repo_id IS NOT NULL;
CREATE INDEX webhooks_org_idx ON core.webhooks (org_id) WHERE org_id IS NOT NULL;

-- C6, C7: una consegna (= un X-GitStack-Delivery) di un evento a un
-- webhook, con l'ultimo tentativo. Una "Redeliver" crea una consegna nuova con
-- redelivery_of. Conservate 30 giorni (C7), poi le elimina il job di pulizia.
--   pending  da fare, o in attesa del prossimo tentativo (next_attempt_at)
--   success  2xx
--   failed   tentativi esauriti (8 in ~24 ore) o errore non ritentabile
--   gone     il destinatario ha risposto 410 Gone: consegna fermata
CREATE TABLE core.webhook_deliveries (
    id UUID PRIMARY KEY,
    webhook_id UUID NOT NULL REFERENCES core.webhooks (id) ON DELETE CASCADE,
    -- Tipo di webhook (push, issues, issue_comment, repository), azione (es.
    -- opened) ed envelope.id dell'evento di dominio da cui nasce (idempotenza
    -- del consumer).
    event TEXT NOT NULL CHECK (event IN ('push', 'issues', 'issue_comment', 'repository')),
    action TEXT NOT NULL DEFAULT '',
    source_event_id UUID NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'success', 'failed', 'gone')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt BETWEEN 0 AND 8),
    next_attempt_at TIMESTAMPTZ NULL,
    status_code INTEGER NULL CHECK (status_code BETWEEN 100 AND 599),
    duration_ms INTEGER NULL CHECK (duration_ms >= 0),
    error TEXT NOT NULL DEFAULT '' CHECK (char_length(error) <= 1024),
    request_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload JSONB NOT NULL,
    response_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- C8: la risposta e' troncata a 4 KB.
    response_body TEXT NOT NULL DEFAULT '' CHECK (octet_length(response_body) <= 4096),
    response_truncated BOOLEAN NOT NULL DEFAULT false,
    redelivery_of UUID NULL REFERENCES core.webhook_deliveries (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ NULL
);
CREATE INDEX webhook_deliveries_webhook_idx ON core.webhook_deliveries (webhook_id, created_at DESC, id);
CREATE INDEX webhook_deliveries_due_idx ON core.webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_created_idx ON core.webhook_deliveries (created_at);
-- Un evento di dominio non crea due consegne per lo stesso webhook
-- (le redelivery sono escluse).
CREATE UNIQUE INDEX webhook_deliveries_source_key ON core.webhook_deliveries (webhook_id, source_event_id)
    WHERE source_event_id IS NOT NULL AND redelivery_of IS NULL;

-- C3, C4, C9: casella notifiche. user_id puo' essere una persona o un agente
-- (stessa casella, C4). Il consumer decide a chi notificare (iscritti,
-- watch, mai l'attore, C3); le notifiche sparite per perdita d'accesso al repo
-- (C9) le elimina l'applicazione. Le lette si eliminano dopo 90 giorni.
CREATE TABLE core.notifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    reason TEXT NOT NULL CHECK (reason IN (
        'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook')),
    repo_id UUID NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    issue_id UUID NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    -- Solo reason='webhook' (webhook disattivato, C7): il webhook e' di chi lo gestisce.
    webhook_id UUID NULL REFERENCES core.webhooks (id) ON DELETE CASCADE,
    comment_id UUID NULL REFERENCES core.issue_comments (id) ON DELETE CASCADE,
    -- Chi ha causato la notifica (NULL = sistema, es. chiusura da commit).
    actor_id UUID NULL,
    -- Nome dell'evento di dominio (es. issue.closed) e dettagli per il testo.
    event_name TEXT NOT NULL DEFAULT '',
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Letta = read_at valorizzato; archiviata = archived_at valorizzato.
    read_at TIMESTAMPTZ NULL,
    archived_at TIMESTAMPTZ NULL,
    -- C5: email da mandare dopo il raggruppamento di pochi secondi per issue;
    -- email_sent_at la chiude. Senza SMTP o per gli agenti resta NULL.
    email_due_at TIMESTAMPTZ NULL,
    email_sent_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notifications_webhook_matches_reason CHECK ((reason = 'webhook') = (webhook_id IS NOT NULL)),
    CONSTRAINT notifications_issue_needs_repo CHECK (issue_id IS NULL OR repo_id IS NOT NULL)
);
-- Elenco: ordine per data decrescente, con filtro sullo stato.
CREATE INDEX notifications_user_idx ON core.notifications (user_id, created_at DESC, id);
CREATE INDEX notifications_user_unread_idx ON core.notifications (user_id, created_at DESC, id)
    WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX notifications_user_reason_idx ON core.notifications (user_id, reason, created_at DESC);
CREATE INDEX notifications_repo_idx ON core.notifications (repo_id, user_id) WHERE repo_id IS NOT NULL;
-- Pulizia a 90 giorni delle lette (C9).
CREATE INDEX notifications_read_at_idx ON core.notifications (read_at) WHERE read_at IS NOT NULL;
-- Email da inviare (raggruppamento di pochi secondi per issue, C5).
CREATE INDEX notifications_email_due_idx ON core.notifications (email_due_at)
    WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL;

-- C2: commit che citano una issue. Una riga per (issue, repo del commit,
-- sha). close_keyword e' valorizzata se il messaggio ha close/fix/resolve
-- davanti al riferimento; closed_applied_at quando la chiusura e' stata
-- eseguita (solo dal branch principale, solo con `write`): serve a non
-- richiudere con lo stesso commit una issue riaperta.
CREATE TABLE core.issue_commit_links (
    id UUID PRIMARY KEY,
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    commit_repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    -- Ref del push in cui il commit e' stato visto per la prima volta (es. refs/heads/fix-12).
    ref TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '' CHECK (char_length(subject) <= 512),
    author_name TEXT NOT NULL DEFAULT '',
    committed_at TIMESTAMPTZ NULL,
    pusher_id UUID NULL,
    close_keyword TEXT NULL CHECK (close_keyword IN ('close', 'fix', 'resolve')),
    -- Il commit e' (o e' entrato) nel branch principale del suo repo.
    on_default_branch BOOLEAN NOT NULL DEFAULT false,
    closed_applied_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT issue_commit_links_key UNIQUE (issue_id, commit_repo_id, commit_sha),
    CONSTRAINT issue_commit_links_closed_needs_keyword CHECK (closed_applied_at IS NULL OR close_keyword IS NOT NULL)
);
CREATE INDEX issue_commit_links_commit_idx ON core.issue_commit_links (commit_repo_id, commit_sha);

-- C1: riferimenti `#n` / `owner/repo#n` fra issues (o PR) e commenti. Sorgente
-- e destinazione possono stare in repo diversi; la visibilita' (vede
-- entrambi i repo) si controlla in lettura. source_comment_id NULL = il
-- riferimento sta nel titolo/testo della sorgente.
CREATE TABLE core.issue_references (
    id UUID PRIMARY KEY,
    target_issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('issue', 'pull_request')),
    source_repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    source_number BIGINT NOT NULL CHECK (source_number >= 1),
    source_comment_id UUID NULL REFERENCES core.issue_comments (id) ON DELETE CASCADE,
    actor_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_references_key ON core.issue_references
    (target_issue_id, source_kind, source_repo_id, source_number,
     COALESCE(source_comment_id, '00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX issue_references_source_idx ON core.issue_references (source_repo_id, source_number);
