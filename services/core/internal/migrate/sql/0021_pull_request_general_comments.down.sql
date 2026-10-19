-- Rollback di 0021: i commenti generali non hanno conversazione, quindi si
-- perdono; i segnaposto tornano commenti con un testo fisso.
DELETE FROM core.pull_request_comments WHERE thread_id IS NULL;
UPDATE core.pull_request_comments SET body = '[eliminato]' WHERE deleted;

DROP INDEX IF EXISTS core.pull_request_comments_pr_idx;
ALTER TABLE core.pull_request_comments
    DROP CONSTRAINT IF EXISTS pull_request_comments_deleted_has_thread,
    DROP CONSTRAINT IF EXISTS pull_request_comments_body_check;
ALTER TABLE core.pull_request_comments
    ADD CONSTRAINT pull_request_comments_body_check CHECK (char_length(body) BETWEEN 1 AND 65536),
    ALTER COLUMN thread_id SET NOT NULL,
    DROP COLUMN IF EXISTS deleted,
    DROP COLUMN IF EXISTS pull_request_id;
