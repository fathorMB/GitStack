-- v3-a (GIT-349, H7/H10): firme al deploy. «Solo immagini firmate» è un'impostazione
-- dell'istanza (stessa riga singleton delle capacità delle app); le chiavi cosign fidate
-- aggiuntive le gestisce l'amministratore. La chiave della CI dell'istanza non sta qui: la tiene
-- il servizio git e c'è sempre.
ALTER TABLE core.app_capacity_settings ADD COLUMN signed_images_only BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE core.app_trusted_keys (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    -- SHA-256 della chiave pubblica DER (PKIX) in esadecimale: la stessa chiave non entra due volte.
    fingerprint TEXT NOT NULL UNIQUE CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    public_key TEXT NOT NULL CHECK (char_length(public_key) BETWEEN 1 AND 4096),
    created_by JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL
);
