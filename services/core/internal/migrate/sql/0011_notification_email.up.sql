-- M-06/F (GIT-134): email delle notifiche (C5). 0006 ha gia' email_due_at e
-- email_sent_at; qui i tentativi, per ritentare un numero limitato di volte,
-- e il segno dell'invio definitivamente fallito (nessuna email_sent_at falsa).
--   email_due_at   quando mandare l'email (NULL = niente email o chiusa)
--   email_sent_at  email consegnata al server SMTP
--   email_attempts invii falliti finora
--   email_failed_at tentativi esauriti: l'email non parte piu'
ALTER TABLE core.notifications
    ADD COLUMN email_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN email_failed_at TIMESTAMPTZ NULL;
