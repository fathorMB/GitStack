-- Rollback di 0020: si perde il metodo di merge proposto.
ALTER TABLE core.branch_settings
    DROP CONSTRAINT IF EXISTS branch_settings_default_allowed,
    DROP COLUMN IF EXISTS default_merge_method;
DELETE FROM core.pull_request_events WHERE type = 'branch_deleted';
ALTER TABLE core.pull_request_events DROP CONSTRAINT IF EXISTS pull_request_events_type_check;
ALTER TABLE core.pull_request_events ADD CONSTRAINT pull_request_events_type_check CHECK (type IN (
    'opened', 'renamed', 'edited', 'retargeted', 'ready_for_review', 'converted_to_draft',
    'closed', 'reopened', 'merged'));
