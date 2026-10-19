-- v1.1 (GIT-303): chi ha scritto uno stato di commit. via_token_* come su issues e
-- PR (0008, 0018): il token con cui il gateway ha autenticato il chiamante, NULL
-- per le sessioni web. by_agent: l'autore e' un account agent (come nei commenti
-- e nelle review delle PR). Le righe esistenti restano senza token e non-agent.
ALTER TABLE core.commit_statuses
    ADD COLUMN via_token_id UUID NULL,
    ADD COLUMN via_token_name TEXT NULL,
    ADD COLUMN by_agent BOOLEAN NOT NULL DEFAULT false;
