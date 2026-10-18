-- Rollback di 0018_pull_request_model: si perdono review, conversazioni,
-- revisori richiesti, stati dei commit, impostazioni del branch e i file
-- Viewed; core.pull_requests (e i suoi numeri) torna com'era nella v1.0.
DROP TABLE IF EXISTS core.pull_request_viewed_files;
DROP TABLE IF EXISTS core.branch_settings;
DROP TABLE IF EXISTS core.commit_statuses;
DROP TABLE IF EXISTS core.pull_request_review_requests;
DROP TABLE IF EXISTS core.pull_request_comments;
DROP TABLE IF EXISTS core.pull_request_threads;
DROP TABLE IF EXISTS core.pull_request_reviews;
DROP INDEX IF EXISTS core.pull_requests_repo_state_idx;
ALTER TABLE core.pull_requests
    DROP CONSTRAINT IF EXISTS pull_requests_merged_not_draft,
    DROP CONSTRAINT IF EXISTS pull_requests_merge_fields_only_if_merged,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS closed_at,
    DROP COLUMN IF EXISTS merged_at,
    DROP COLUMN IF EXISTS merged_by,
    DROP COLUMN IF EXISTS merge_commit_sha,
    DROP COLUMN IF EXISTS merge_method,
    DROP COLUMN IF EXISTS head_sha,
    DROP COLUMN IF EXISTS base_sha,
    DROP COLUMN IF EXISTS draft,
    DROP COLUMN IF EXISTS body;
