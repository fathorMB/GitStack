-- Rollback di 0004_issues. Si perdono tutte le issues, i commenti, le
-- etichette, le milestone e i metadati degli allegati (i file sul volume
-- non vengono toccati: vanno rimossi a mano). core.repo_counters resta, con
-- le righe create dalla migrazione; torna solo senza la colonna delle
-- milestone.
DROP TRIGGER IF EXISTS pull_requests_number_not_taken ON core.pull_requests;
DROP TABLE IF EXISTS core.issue_attachments;
DROP TABLE IF EXISTS core.issue_assignees;
DROP TABLE IF EXISTS core.issue_labels;
DROP TABLE IF EXISTS core.issue_events;
DROP TABLE IF EXISTS core.issue_text_versions;
DROP TABLE IF EXISTS core.issue_comments;
DROP TABLE IF EXISTS core.issues;
DROP TABLE IF EXISTS core.labels;
DROP TABLE IF EXISTS core.milestones;
DROP FUNCTION IF EXISTS core.seed_default_labels(UUID);
DROP FUNCTION IF EXISTS core.check_assignee_limit();
DROP FUNCTION IF EXISTS core.check_number_not_taken();
ALTER TABLE core.repo_counters DROP COLUMN IF EXISTS next_milestone_number;
