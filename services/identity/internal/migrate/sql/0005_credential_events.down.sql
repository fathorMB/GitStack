-- Rollback di 0005.
ALTER TABLE identity.api_tokens DROP COLUMN IF EXISTS expiry_notified_at;
DROP TABLE IF EXISTS identity.credential_events;
