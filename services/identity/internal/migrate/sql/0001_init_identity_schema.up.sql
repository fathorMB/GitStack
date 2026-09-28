-- Schema dedicato "identity" (D6): lo crea internal/migrate.EnsureSchema, non
-- questa migrazione (stessa ragione di core: un ruolo senza CREATE sul
-- database non puo' eseguire nemmeno CREATE SCHEMA IF NOT EXISTS).
--
-- Regola di tutto il modello: nessun segreto in chiaro. Password, token di
-- sessione e token personali sono salvati solo come hash; il segreto client
-- dei provider OIDC e' salvato cifrato (AES-GCM applicativo, chiave fuori dal
-- database). Le colonne *_hash / *_enc lo dicono nel nome.
--
-- Gli identificatori sono UUID generati dall'applicazione (come in core).
-- Gli username sono minuscoli per vincolo di formato, quindi l'unicita' e'
-- diretta: niente estensione citext, che richiederebbe privilegi che il
-- ruolo del servizio non ha.

CREATE TABLE identity.users (
    id           UUID PRIMARY KEY,
    username     TEXT NOT NULL,
    email        TEXT,
    display_name TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    avatar_url   TEXT,
    -- 'human' per le persone, 'agent' per gli agenti (D5: utenti e agenti si
    -- autenticano con lo stesso modello, gli agenti solo con token).
    kind         TEXT NOT NULL DEFAULT 'human',
    is_admin     BOOLEAN NOT NULL DEFAULT false,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_kind_check CHECK (kind IN ('human', 'agent')),
    CONSTRAINT users_username_format CHECK (username ~ '^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$'),
    CONSTRAINT users_email_not_blank CHECK (email IS NULL OR email <> '')
);
CREATE UNIQUE INDEX users_username_key ON identity.users (username);
CREATE UNIQUE INDEX users_email_key ON identity.users (lower(email)) WHERE email IS NOT NULL;

-- Credenziali di accesso locali. Oggi solo 'password'; la chiave (user_id,
-- kind) permette di aggiungerne altre (es. TOTP) senza cambiare tabella.
-- secret_hash e' una stringa PHC (argon2id: parametri e salt inclusi), mai la
-- password.
CREATE TABLE identity.credentials (
    user_id     UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    secret_hash TEXT NOT NULL,
    must_change BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind),
    CONSTRAINT credentials_kind_check CHECK (kind IN ('password')),
    CONSTRAINT credentials_hash_not_blank CHECK (secret_hash <> '')
);

-- Sessioni web/CLI. L'identificatore opaco della sessione (cookie
-- gst_session) non e' salvato: qui c'e' solo il suo SHA-256 (32 byte).
CREATE TABLE identity.sessions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    token_hash   BYTEA NOT NULL,
    auth_method  TEXT NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip_address   INET,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,
    CONSTRAINT sessions_auth_method_check CHECK (auth_method IN ('password', 'oidc')),
    CONSTRAINT sessions_token_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX sessions_token_hash_key ON identity.sessions (token_hash);
CREATE INDEX sessions_user_id_idx ON identity.sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON identity.sessions (expires_at);

-- Token personali (formato "gst_<random>"). Solo l'hash SHA-256 e' salvato;
-- token_hint sono gli ultimi 4 caratteri, per riconoscerlo nell'elenco.
-- Gli scope sono il catalogo del contratto (api/openapi.yaml, TokenScope).
CREATE TABLE identity.api_tokens (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   BYTEA NOT NULL,
    token_hint   TEXT NOT NULL,
    scopes       TEXT[] NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    CONSTRAINT api_tokens_name_not_blank CHECK (name <> ''),
    CONSTRAINT api_tokens_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT api_tokens_hint_len CHECK (char_length(token_hint) = 4),
    CONSTRAINT api_tokens_scopes_not_empty CHECK (cardinality(scopes) > 0),
    CONSTRAINT api_tokens_scopes_known CHECK (scopes <@ ARRAY[
        'read:user', 'write:user', 'read:org', 'write:org', 'admin:org',
        'read:resource', 'write:resource'
    ]::text[])
);
CREATE UNIQUE INDEX api_tokens_token_hash_key ON identity.api_tokens (token_hash);
CREATE UNIQUE INDEX api_tokens_user_name_key ON identity.api_tokens (user_id, name);
CREATE INDEX api_tokens_user_id_idx ON identity.api_tokens (user_id);

-- Chiavi SSH pubbliche (non sono segreti). Il fingerprint SHA256 unico e'
-- la chiave con cui il servizio git risolve l'utente all'accesso SSH.
CREATE TABLE identity.ssh_keys (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    title              TEXT NOT NULL,
    key_type           TEXT NOT NULL,
    public_key         TEXT NOT NULL,
    fingerprint_sha256 TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at       TIMESTAMPTZ,
    CONSTRAINT ssh_keys_title_not_blank CHECK (title <> ''),
    CONSTRAINT ssh_keys_type_check CHECK (key_type IN ('ssh-ed25519', 'ssh-rsa', 'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521', 'sk-ssh-ed25519@openssh.com')),
    CONSTRAINT ssh_keys_fingerprint_format CHECK (fingerprint_sha256 ~ '^SHA256:[A-Za-z0-9+/]{43}$')
);
CREATE UNIQUE INDEX ssh_keys_fingerprint_key ON identity.ssh_keys (fingerprint_sha256);
CREATE UNIQUE INDEX ssh_keys_user_title_key ON identity.ssh_keys (user_id, title);
CREATE INDEX ssh_keys_user_id_idx ON identity.ssh_keys (user_id);

-- Provider OIDC esterni (D5). client_secret_enc e' il segreto cifrato
-- dall'applicazione (nonce + ciphertext AES-256-GCM); enc_key_id dice con
-- quale chiave (rotazione). Mai il segreto in chiaro.
CREATE TABLE identity.oidc_providers (
    id                UUID PRIMARY KEY,
    slug              TEXT NOT NULL,
    display_name      TEXT NOT NULL,
    issuer_url        TEXT NOT NULL,
    client_id         TEXT NOT NULL,
    client_secret_enc BYTEA NOT NULL,
    enc_key_id        TEXT NOT NULL,
    scopes            TEXT[] NOT NULL DEFAULT ARRAY['openid', 'profile', 'email']::text[],
    enabled           BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT oidc_providers_slug_format CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    CONSTRAINT oidc_providers_secret_not_empty CHECK (octet_length(client_secret_enc) > 0)
);
CREATE UNIQUE INDEX oidc_providers_slug_key ON identity.oidc_providers (slug);

-- Identita' esterna collegata a un utente: (provider, subject) e' l'identita'
-- stabile lato IdP (l'email puo' cambiare).
CREATE TABLE identity.oidc_identities (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    provider_id   UUID NOT NULL REFERENCES identity.oidc_providers (id) ON DELETE CASCADE,
    subject       TEXT NOT NULL,
    email         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ,
    CONSTRAINT oidc_identities_subject_not_blank CHECK (subject <> '')
);
CREATE UNIQUE INDEX oidc_identities_provider_subject_key ON identity.oidc_identities (provider_id, subject);
CREATE INDEX oidc_identities_user_id_idx ON identity.oidc_identities (user_id);
