-- v3-a (GIT-377, H7 del 2026-10-26): deploy automatico anche da una regola sui tag. Ogni app segue
-- un branch (follow_branch) oppure una regola sui tag (follow_tag_pattern, un glob come
-- v*-test.*), mai tutte e due.
ALTER TABLE core.apps ADD COLUMN follow_tag_pattern TEXT NULL
    CHECK (follow_tag_pattern IS NULL OR char_length(follow_tag_pattern) BETWEEN 1 AND 128);
ALTER TABLE core.apps ADD CONSTRAINT apps_follow_one_source
    CHECK (follow_branch IS NULL OR follow_tag_pattern IS NULL);

ALTER TABLE core.app_versions DROP CONSTRAINT app_versions_trigger_check;
ALTER TABLE core.app_versions ADD CONSTRAINT app_versions_trigger_check
    CHECK (trigger IN ('create', 'manual', 'branch_push', 'tag_push', 'config', 'rollback'));
