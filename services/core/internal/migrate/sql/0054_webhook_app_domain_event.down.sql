-- Torna all'elenco della 0044 (senza app_domain). Le consegne di app_domain si tolgono e i webhook che
-- restavano solo con questo evento vengono eliminati.
DELETE FROM core.webhook_deliveries WHERE event = 'app_domain';
DELETE FROM core.webhooks
	WHERE events = ARRAY['app_domain']::TEXT[];
UPDATE core.webhooks SET events = array_remove(events, 'app_domain')
	WHERE events && ARRAY['app_domain']::TEXT[];
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment'));
ALTER TABLE core.webhooks DROP CONSTRAINT IF EXISTS webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment']::TEXT[]);
