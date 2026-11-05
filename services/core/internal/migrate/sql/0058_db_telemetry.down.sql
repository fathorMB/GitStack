-- v3-d (GIT-568): telemetria dei database e webhook database: via con tabelle e vincoli.

DROP TABLE IF EXISTS core.managed_db_log_lines;
DROP TABLE IF EXISTS core.managed_db_metric_samples;

-- Torna all'elenco della 0054 (senza database). Le consegne di database si tolgono e i webhook che
-- restavano solo con questo evento vengono eliminati.
DELETE FROM core.webhook_deliveries WHERE event = 'database';
DELETE FROM core.webhooks
	WHERE events = ARRAY['database']::TEXT[];
UPDATE core.webhooks SET events = array_remove(events, 'database')
	WHERE events && ARRAY['database']::TEXT[];
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment','app_domain'));
ALTER TABLE core.webhooks DROP CONSTRAINT IF EXISTS webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job','deployment','app_domain']::TEXT[]);
