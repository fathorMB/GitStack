DELETE FROM core.webhook_deliveries WHERE event = 'release';
UPDATE core.webhooks SET events = array_remove(events, 'release') WHERE 'release' = ANY(events);
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status'));
ALTER TABLE core.webhooks DROP CONSTRAINT IF EXISTS webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status']::TEXT[]);
DROP TABLE IF EXISTS core.release_assets;
DROP TABLE IF EXISTS core.releases;
