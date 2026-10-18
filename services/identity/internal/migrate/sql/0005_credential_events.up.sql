-- F13 (GIT-239): avvisi all'utente quando cambia una sua credenziale (ASVS
-- V2.2.3, V2.3.3, V2.5.5). Identity scrive qui un evento per ogni cambio di
-- password, nuovo token personale, nuova chiave SSH e per ogni token in
-- scadenza; core li legge con GET /internal/credential-events, li trasforma in
-- notifica in-app ed email e conferma con POST .../ack (delivered_at).
-- Nessun segreto: solo il tipo, il nome del token o della chiave e le date.
CREATE TABLE identity.credential_events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    detail       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    CONSTRAINT credential_events_kind_known CHECK (kind IN (
        'password_changed', 'password_reset', 'token_created', 'token_expiring', 'ssh_key_added'))
);
CREATE INDEX credential_events_pending_idx ON identity.credential_events (id) WHERE delivered_at IS NULL;

-- Un solo avviso di scadenza per token.
ALTER TABLE identity.api_tokens ADD COLUMN expiry_notified_at TIMESTAMPTZ;
