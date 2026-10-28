ALTER TABLE core.app_domains
    DROP CONSTRAINT IF EXISTS app_domains_custom_cert_chk,
    DROP COLUMN IF EXISTS cert_pem,
    DROP COLUMN IF EXISTS cert_key_ciphertext,
    DROP COLUMN IF EXISTS cert_key_nonce,
    DROP COLUMN IF EXISTS cert_key_id;
