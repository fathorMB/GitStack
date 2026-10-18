-- v1.1 (GIT-263): metodo di merge proposto, per repo (PR3). Il default e' squash;
-- una riga che ammette solo il merge commit propone quello.
ALTER TABLE core.branch_settings
    ADD COLUMN default_merge_method TEXT NOT NULL DEFAULT 'squash'
        CONSTRAINT branch_settings_default_method CHECK (default_merge_method IN ('merge', 'squash'));
UPDATE core.branch_settings SET default_merge_method = merge_methods[1]
    WHERE NOT (default_merge_method = ANY (merge_methods));
ALTER TABLE core.branch_settings
    ADD CONSTRAINT branch_settings_default_allowed CHECK (default_merge_method = ANY (merge_methods));

-- GIT-263: la cronologia della PR registra anche l'eliminazione del branch di origine.
ALTER TABLE core.pull_request_events DROP CONSTRAINT IF EXISTS pull_request_events_type_check;
ALTER TABLE core.pull_request_events ADD CONSTRAINT pull_request_events_type_check CHECK (type IN (
    'opened', 'renamed', 'edited', 'retargeted', 'ready_for_review', 'converted_to_draft',
    'closed', 'reopened', 'merged', 'branch_deleted'));
