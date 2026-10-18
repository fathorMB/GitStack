-- v1.1 (GIT-261): cronologia degli eventi della PR e origine "via token" (PR1, PR6).
-- Gli *_id utente non hanno FK verso identity, come in core.issue_events.
ALTER TABLE core.pull_requests
    ADD COLUMN via_token_id UUID NULL,
    ADD COLUMN via_token_name TEXT NULL;

CREATE TABLE core.pull_request_events (
    id UUID PRIMARY KEY,
    pull_request_id UUID NOT NULL REFERENCES core.pull_requests (id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN (
        'opened', 'renamed', 'edited', 'retargeted', 'ready_for_review', 'converted_to_draft',
        'closed', 'reopened', 'merged')),
    actor_id UUID NULL,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_request_events_pr_idx ON core.pull_request_events (pull_request_id, created_at, id);

-- Una sola PR aperta per coppia di branch (PR1): lo garantisce lo schema, anche
-- con richieste concorrenti. Le PR chiuse o unite non contano.
CREATE UNIQUE INDEX pull_requests_open_branches_key
    ON core.pull_requests (repo_id, source_branch, target_branch) WHERE state = 'open';
