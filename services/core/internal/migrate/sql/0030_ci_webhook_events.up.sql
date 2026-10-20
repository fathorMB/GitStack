-- v2-a (GIT-281, CI6): i webhook workflow_run e workflow_job. Entrano nell'elenco
-- degli eventi selezionabili dei webhook del repo e in quello delle consegne, come
-- gli altri eventi di dominio (il contratto WebhookEvent li dichiara già).
ALTER TABLE core.webhooks DROP CONSTRAINT webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1 AND events <@ ARRAY['push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job']::TEXT[]);
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push','issues','issue_comment','repository','pull_request','pull_request_review','commit_status','release','workflow_run','workflow_job'));
