-- Rollback di 0022: via notifiche, iscrizioni, link di chiusura e webhook delle PR.
DELETE FROM core.webhook_deliveries WHERE event IN ('pull_request', 'pull_request_review', 'commit_status');
-- Un webhook solo dei tipi nuovi non ha piu' senso: via, poi gli altri perdono i tipi nuovi.
DELETE FROM core.webhooks WHERE NOT events && ARRAY['push', 'issues', 'issue_comment', 'repository']::TEXT[];
UPDATE core.webhooks SET events = ARRAY(
        SELECT e FROM unnest(events) AS e WHERE e IN ('push', 'issues', 'issue_comment', 'repository'))
    WHERE NOT events <@ ARRAY['push', 'issues', 'issue_comment', 'repository']::TEXT[];
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push', 'issues', 'issue_comment', 'repository'));
ALTER TABLE core.webhooks DROP CONSTRAINT webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1
    AND events <@ ARRAY['push', 'issues', 'issue_comment', 'repository']::TEXT[]);

DROP INDEX IF EXISTS core.notifications_pull_idx;
DELETE FROM core.notifications WHERE pull_request_id IS NOT NULL OR reason = 'review_requested';
DELETE FROM core.notification_preferences WHERE reason = 'review_requested';
ALTER TABLE core.notification_preferences DROP CONSTRAINT notification_preferences_reason_check;
ALTER TABLE core.notification_preferences ADD CONSTRAINT notification_preferences_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror'));
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror', 'credential'));
ALTER TABLE core.notifications DROP CONSTRAINT notifications_pull_needs_repo;
ALTER TABLE core.notifications DROP COLUMN pull_request_id;

ALTER TABLE core.pull_requests DROP COLUMN has_conflicts;
DROP TABLE core.pull_request_subscriptions;
DROP TABLE core.pull_request_closing_issues;
