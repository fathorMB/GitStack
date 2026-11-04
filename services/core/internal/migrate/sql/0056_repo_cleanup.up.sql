-- GIT-530 (R2 rivista il 4/11): l'eliminazione di un repo e' definitiva e immediata, senza cestino.
-- La riga del repo (e con lei il nome, le issue, le PR, le release...) sparisce nella stessa transazione
-- dell'eliminazione; quello che sta fuori da Postgres (disco di git, immagini del registry, app e volumi,
-- allegati, grant in identity) si toglie subito dopo, e se un passo non riesce resta qui, nella coda, per
-- il giro periodico di core. Il nome non dipende da questa coda: e' gia' libero.
CREATE TABLE core.repo_cleanup (
    repo_id UUID PRIMARY KEY, -- l'id del repo: nessuna FK, la riga di core.resources e' gia' stata cancellata
    owner_name TEXT NOT NULL,
    name TEXT NOT NULL,
    -- Le app che il repo aveva: la riga di core.apps cade con il repo, ma namespace e PVC nel cluster si
    -- trovano solo da qui.
    app_ids UUID[] NOT NULL DEFAULT '{}',
    -- 'delete': eliminazione fatta con l'API; 'trash': repo che era nel cestino prima di questa versione.
    source TEXT NOT NULL DEFAULT 'delete' CHECK (source IN ('delete', 'trash')),
    queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);

-- I repo che erano nel cestino all'upgrade: cancellati subito (si libera il nome, per esempio admin/odin su
-- homehub); il resto lo toglie il giro di core, che scrive una riga di log per ciascuno.
INSERT INTO core.repo_cleanup (repo_id, owner_name, name, app_ids, source, queued_at)
    SELECT r.resource_id, r.owner_name, r.name,
           COALESCE((SELECT array_agg(a.id) FROM core.apps a WHERE a.repo_id = r.resource_id), '{}'), 'trash', now()
    FROM core.repositories r WHERE r.deleted_at IS NOT NULL;
DELETE FROM core.resources WHERE id IN (SELECT resource_id FROM core.repositories WHERE deleted_at IS NOT NULL);
