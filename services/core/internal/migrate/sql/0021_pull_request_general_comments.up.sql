-- v1.1 (GIT-300): commenti generali della conversazione di una PR (senza
-- path) e segnaposto dei commenti eliminati (PR5).
-- Scelta: stessa tabella, non una tabella a parte. Un commento generale e uno
-- sulle righe hanno lo stesso aspetto (autore, testo, modifica, by_agent), e
-- l'API li elenca insieme in ordine di tempo: una tabella sola evita UNION e
-- doppi percorsi di modifica/eliminazione. Quindi thread_id diventa nullable
-- e il commento porta direttamente pull_request_id (NOT NULL).
-- Gli *_id utente non hanno FK verso identity, come nel resto di core.

ALTER TABLE core.pull_request_comments
    ADD COLUMN pull_request_id UUID NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    ADD COLUMN deleted BOOLEAN NOT NULL DEFAULT false;

UPDATE core.pull_request_comments c
   SET pull_request_id = t.pull_request_id
  FROM core.pull_request_threads t
 WHERE t.id = c.thread_id;

ALTER TABLE core.pull_request_comments
    ALTER COLUMN pull_request_id SET NOT NULL,
    ALTER COLUMN thread_id DROP NOT NULL;

-- Il segnaposto di un commento eliminato ha il testo vuoto.
ALTER TABLE core.pull_request_comments DROP CONSTRAINT pull_request_comments_body_check;
ALTER TABLE core.pull_request_comments
    ADD CONSTRAINT pull_request_comments_body_check CHECK (
        char_length(body) <= 65536 AND (deleted OR char_length(body) >= 1)),
    ADD CONSTRAINT pull_request_comments_deleted_has_thread CHECK (NOT deleted OR thread_id IS NOT NULL);

CREATE INDEX pull_request_comments_pr_idx ON core.pull_request_comments (pull_request_id, created_at, id);
