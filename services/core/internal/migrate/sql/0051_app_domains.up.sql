-- v3-c (GIT-458): domini propri delle app pubbliche con controllo del DNS.
-- Il dominio e' unico nell'istanza: l'indice sta sul nome normalizzato (minuscolo, senza punto finale).
-- `status` e' lo stato del DNS (pending_dns, verified, error); `failing_since` e' l'inizio del periodo
-- di grazia di un dominio verified il cui DNS ha smesso di puntare all'istanza: la rotta resta finche'
-- il periodo non scade. Le colonne cert_* sono il posto del certificato (emissione ACME e caricamento
-- del cliente arrivano con gli item successivi): oggi nascono letsencrypt/none.
CREATE TABLE core.app_domains (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES core.apps (id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    normalized TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending_dns' CHECK (status IN ('pending_dns', 'verified', 'error')),
    status_reason TEXT NULL,
    last_checked_at TIMESTAMPTZ NULL,
    verified_at TIMESTAMPTZ NULL,
    failing_since TIMESTAMPTZ NULL,
    cert_mode TEXT NOT NULL DEFAULT 'letsencrypt' CHECK (cert_mode IN ('letsencrypt', 'custom')),
    cert_state TEXT NOT NULL DEFAULT 'none' CHECK (cert_state IN ('none', 'issuing', 'valid', 'error')),
    cert_valid_from TIMESTAMPTZ NULL,
    cert_valid_until TIMESTAMPTZ NULL,
    cert_issuer TEXT NULL,
    cert_error TEXT NULL,
    cert_last_attempt_at TIMESTAMPTZ NULL,
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX app_domains_normalized_uniq ON core.app_domains (normalized);
CREATE INDEX app_domains_app_idx ON core.app_domains (app_id, created_at);

-- IP pubblici dichiarati dell'istanza (A/AAAA) contro cui si controlla il DNS dei domini all'apice.
ALTER TABLE core.app_capacity_settings ADD COLUMN public_ips TEXT[] NOT NULL DEFAULT '{}';
