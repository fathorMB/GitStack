-- v2-b (GIT-286, RG1): gli scope del registry OCI. read:registry scarica,
-- write:registry pubblica (implica read), admin:registry elimina (implica write
-- e read). Sono separati da read/write:resource: un token per `docker login` non
-- deve poter toccare il resto del repo, e un token di codice non pubblica immagini.
ALTER TABLE identity.api_tokens DROP CONSTRAINT api_tokens_scopes_known;
ALTER TABLE identity.api_tokens ADD CONSTRAINT api_tokens_scopes_known CHECK (scopes <@ ARRAY[
    'read:user', 'write:user', 'read:org', 'write:org', 'admin:org',
    'read:resource', 'write:resource',
    'read:registry', 'write:registry', 'admin:registry'
]::text[]);
