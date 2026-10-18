-- F11 (GIT-237): secondo fattore TOTP (RFC 6238) per gli account locali.
--
-- Il segreto TOTP e' cifrato dall'applicazione (AES-256-GCM, chiave del
-- keyring fuori dal database): secret_enc = nonce || ciphertext, enc_key_id
-- dice con quale chiave del keyring. Finche' confirmed_at e' NULL
-- l'attivazione e' in attesa del primo codice e non vale come secondo
-- fattore. last_used_step e' l'ultimo passo di 30 secondi accettato: un
-- codice di un passo gia' usato (replay) viene rifiutato.
CREATE TABLE identity.totp_credentials (
    user_id        UUID PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    secret_enc     BYTEA NOT NULL,
    enc_key_id     TEXT NOT NULL,
    confirmed_at   TIMESTAMPTZ,
    last_used_step BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT totp_credentials_secret_not_empty CHECK (octet_length(secret_enc) > 0),
    CONSTRAINT totp_credentials_key_id_not_blank CHECK (enc_key_id <> '')
);

-- Codici di recupero monouso: solo l'hash argon2id salato (stringa PHC, lo
-- stesso del package password), mai il codice (ASVS V2.6.2).
CREATE TABLE identity.totp_recovery_codes (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at    TIMESTAMPTZ,
    CONSTRAINT totp_recovery_codes_hash_phc CHECK (code_hash LIKE '$argon2id$%')
);
CREATE INDEX totp_recovery_codes_user_idx ON identity.totp_recovery_codes (user_id) WHERE used_at IS NULL;
