-- Rollback di 0006: si perdono notifiche, iscrizioni, watch, preferenze,
-- webhook (segreti compresi), consegne e riferimenti. Le righe di
-- issue_events dei tre tipi nuovi vengono eliminate, perche' il vincolo
-- precedente non le ammette.
DROP TABLE IF EXISTS core.issue_references;
DROP TABLE IF EXISTS core.issue_commit_links;
DROP TABLE IF EXISTS core.notifications;
DROP TABLE IF EXISTS core.webhook_deliveries;
DROP TABLE IF EXISTS core.webhooks;
DROP TABLE IF EXISTS core.notification_preferences;
DROP TABLE IF EXISTS core.repo_watches;
DROP TABLE IF EXISTS core.issue_subscriptions;
DELETE FROM core.issue_events WHERE type IN ('referenced_from', 'commit_linked', 'closed_by_commit');
ALTER TABLE core.issue_events DROP CONSTRAINT issue_events_type_check;
ALTER TABLE core.issue_events ADD CONSTRAINT issue_events_type_check CHECK (type IN (
    'opened', 'closed', 'reopened', 'renamed', 'edited', 'labeled', 'unlabeled',
    'assigned', 'unassigned', 'milestoned', 'demilestoned', 'locked', 'unlocked',
    'hidden', 'unhidden', 'comment_deleted', 'referenced'));
