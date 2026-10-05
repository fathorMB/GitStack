-- M-05/C (GIT-103): chiave d'ordine monotona per la cronologia delle issues.
-- created_at viene dall'orologio e non basta: a pari timestamp (o con
-- l'orologio che torna indietro) l'ordine di due eventi sarebbe casuale. seq
-- e' assegnato da Postgres all'INSERT e cresce sempre: la cronologia si
-- ordina per (issue_id, seq).
ALTER TABLE core.issue_events ADD COLUMN seq BIGINT GENERATED ALWAYS AS IDENTITY;
CREATE UNIQUE INDEX issue_events_issue_seq_idx ON core.issue_events (issue_id, seq);
