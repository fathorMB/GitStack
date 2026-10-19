-- v1.1 (GIT-265): notifiche e webhook delle Pull Request (PR8, PR11).
-- Gli *_id utente non hanno FK verso identity, come nel resto di core.

-- PR8: le issues che le parole chiave di C2 nel testo della PR chiudono al
-- merge. Una riga per (PR, issue); keyword e' la parola normalizzata.
CREATE TABLE core.pull_request_closing_issues (
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES core.issues (id) ON DELETE CASCADE,
    keyword TEXT NOT NULL CHECK (keyword IN ('close', 'fix', 'resolve')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, issue_id)
);
CREATE INDEX pull_request_closing_issues_issue_idx ON core.pull_request_closing_issues (issue_id);

-- PR11, C3: chi segue una PR. Come core.issue_subscriptions: subscribed=false
-- e' un Unsubscribe esplicito che l'iscrizione automatica non sovrascrive.
CREATE TABLE core.pull_request_subscriptions (
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    subscribed BOOLEAN NOT NULL DEFAULT true,
    reason TEXT NOT NULL CHECK (reason IN ('author', 'reviewer', 'commenter', 'mentioned', 'manual')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, user_id)
);
CREATE INDEX pull_request_subscriptions_user_idx ON core.pull_request_subscriptions (user_id);

-- PR11: conflitti rilevati sull'ultimo push. Serve a notificare l'autore una
-- volta sola per ogni comparsa dei conflitti, non a ogni push.
ALTER TABLE core.pull_requests ADD COLUMN has_conflicts BOOLEAN NOT NULL DEFAULT false;

-- Notifiche: una notifica puo' riguardare una PR (stessa casella, stessi
-- filtri). Nuovo motivo review_requested: la richiesta di review ha l'email
-- attiva di default come menzioni e assegnazioni (C5, PR11).
ALTER TABLE core.notifications ADD COLUMN pull_request_id UUID NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_pull_needs_repo CHECK (pull_request_id IS NULL OR repo_id IS NOT NULL);
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror', 'credential',
    'review_requested'));
ALTER TABLE core.notification_preferences DROP CONSTRAINT notification_preferences_reason_check;
ALTER TABLE core.notification_preferences ADD CONSTRAINT notification_preferences_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror',
    'review_requested'));
CREATE INDEX notifications_pull_idx ON core.notifications (pull_request_id) WHERE pull_request_id IS NOT NULL;

-- Webhook: tre eventi nuovi (PR11).
ALTER TABLE core.webhooks DROP CONSTRAINT webhooks_events_check;
ALTER TABLE core.webhooks ADD CONSTRAINT webhooks_events_check CHECK (
    cardinality(events) >= 1
    AND events <@ ARRAY['push', 'issues', 'issue_comment', 'repository', 'pull_request', 'pull_request_review', 'commit_status']::TEXT[]);
ALTER TABLE core.webhook_deliveries DROP CONSTRAINT webhook_deliveries_event_check;
ALTER TABLE core.webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_check CHECK (
    event IN ('push', 'issues', 'issue_comment', 'repository', 'pull_request', 'pull_request_review', 'commit_status'));
