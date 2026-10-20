-- Torna all'elenco della 0025 (senza workflow_run e workflow_job). Come per la
-- 0025, un webhook che restava solo con questi eventi non passa il vincolo.
DELETE FROM core.webhook_deliveries WHERE event IN ('workflow_run', 'workflow_job');
UPDATE core.webhooks SET events = array_remove(array_remove(events, 'workflow_run'), 'workflow_job')
	WHERE events && ARRAY['workflow_run', 'workflow_job']::TEXT[];
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release'));
ALTER TABLE core.webhooks DROP CONSTRAINT IF EXISTS webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release']::TEXT[]);
