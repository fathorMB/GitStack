-- v2-a (GIT-314): richiesta di annullamento di un job in corso. Un job in
-- in_progress a cui arriva un cancel resta in_progress con cancel_requested_at
-- valorizzato; il runner (GIT-277) lo legge e lo chiude con CompleteJob(…, cancelled).
ALTER TABLE core.ci_jobs ADD COLUMN cancel_requested_at TIMESTAMPTZ NULL;
