-- Rollback di 0006: i token con uno scope del registry perdono quegli scope; un
-- token che ne resta senza viene revocato (la tabella vieta gli scope vuoti).
UPDATE identity.api_tokens
   SET scopes = ARRAY(SELECT s FROM unnest(scopes) AS s WHERE s NOT IN ('read:registry', 'write:registry', 'admin:registry'))
 WHERE scopes && ARRAY['read:registry', 'write:registry', 'admin:registry']::text[]
   AND cardinality(ARRAY(SELECT s FROM unnest(scopes) AS s WHERE s NOT IN ('read:registry', 'write:registry', 'admin:registry'))) > 0;
DELETE FROM identity.api_tokens WHERE scopes && ARRAY['read:registry', 'write:registry', 'admin:registry']::text[];
ALTER TABLE identity.api_tokens DROP CONSTRAINT api_tokens_scopes_known;
ALTER TABLE identity.api_tokens ADD CONSTRAINT api_tokens_scopes_known CHECK (scopes <@ ARRAY[
    'read:user', 'write:user', 'read:org', 'write:org', 'admin:org',
    'read:resource', 'write:resource'
]::text[]);
