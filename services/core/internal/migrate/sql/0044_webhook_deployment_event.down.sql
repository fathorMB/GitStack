-- Torna all'elenco della 0030 (senza deployment). Come per la 0030, le consegne di deployment si tolgono
-- e i webhook che restavano solo con questo evento non passano il vincolo: si tolgono gli eventi.
DELETE FROM core.webhook_deliveries WHERE event = 'deployment';
UPDATE core.webhooks SET events = array_remove(events, 'deployment')
	WHERE events && ARRAY['deployment']::TEXT[];
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job'));
ALTER TABLE core.webhooks DROP CONSTRAINT IF EXISTS webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job']::TEXT[]);
