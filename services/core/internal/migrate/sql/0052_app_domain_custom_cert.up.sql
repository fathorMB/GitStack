-- v3-c (GIT-460): certificato caricato dal cliente per un dominio proprio.
-- cert_pem e' la catena pubblica (foglia per prima); la chiave privata e' solo cifrata (AES-256-GCM, stesso
-- Keyring dei segreti delle app, l'id del dominio e' il dato autenticato) e non si rilegge da nessuna API.
-- Le colonne sono tutte NULL per i domini letsencrypt; hanno senso solo con cert_mode = 'custom'.
ALTER TABLE core.app_domains
    ADD COLUMN cert_pem TEXT NULL,
    ADD COLUMN cert_key_ciphertext BYTEA NULL,
    ADD COLUMN cert_key_nonce BYTEA NULL,
    ADD COLUMN cert_key_id TEXT NULL,
    ADD CONSTRAINT app_domains_custom_cert_chk CHECK (cert_mode <> 'custom' OR (cert_pem IS NOT NULL AND cert_key_ciphertext IS NOT NULL AND cert_key_nonce IS NOT NULL AND cert_key_id IS NOT NULL));
